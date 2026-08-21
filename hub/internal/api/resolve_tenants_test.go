package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// viewerOn builds an identity holding a viewer grant on each scope.
func viewerOn(scopes ...string) *auth.Identity {
	id := &auth.Identity{UserID: "u1"}
	for _, s := range scopes {
		id.Grants = append(id.Grants, auth.Grant{Scope: s, Role: auth.RoleViewer})
	}
	return id
}

func TestResolveTenants(t *testing.T) {
	boom := errors.New("clickhouse down")
	agg := storage.Project{ID: "team", Members: []string{"a", "b"}}
	leafRow := storage.Project{ID: "payments", Members: []string{}}

	tests := []struct {
		name       string
		projects   []storage.Project
		storeErr   error
		project    string
		id         *auth.Identity
		want       []string
		wantStatus int   // apiError status when non-zero
		wantErr    error // exact error when non-nil
	}{
		{
			name:    "leaf: no db row resolves to itself",
			project: "payments",
			id:      viewerOn("payments"),
			want:    []string{"payments"},
		},
		{
			name:     "leaf: db row without members resolves to itself",
			projects: []storage.Project{leafRow},
			project:  "payments",
			id:       viewerOn("payments"),
			want:     []string{"payments"},
		},
		{
			name:     "aggregate: nil identity (auth disabled) keeps all members",
			projects: []storage.Project{agg},
			project:  "team",
			want:     []string{"a", "b"},
		},
		{
			name:     "aggregate: wildcard grant keeps all members",
			projects: []storage.Project{agg},
			project:  "team",
			id:       viewerOn("*"),
			want:     []string{"a", "b"},
		},
		{
			name:     "aggregate: members intersected with grants",
			projects: []storage.Project{agg},
			project:  "team",
			id:       viewerOn("b", "unrelated"),
			want:     []string{"b"},
		},
		{
			name:       "aggregate: empty intersection is 403",
			projects:   []storage.Project{agg},
			project:    "team",
			id:         viewerOn("unrelated"),
			wantStatus: http.StatusForbidden,
		},
		{
			name:     "store error fails closed",
			storeErr: boom,
			project:  "payments",
			id:       viewerOn("payments"),
			wantErr:  boom,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &storagetest.Fake{ProjectsErr: tt.storeErr}
			for _, p := range tt.projects {
				_ = f.SaveProject(context.Background(), p)
			}
			a := &API{provider: func() storage.Store { return f }}

			got, err := a.resolveTenants(context.Background(), tt.project, tt.id)
			if tt.wantStatus != 0 {
				var ae *apiError
				if !errors.As(err, &ae) || ae.status != tt.wantStatus {
					t.Fatalf("err = %v, want apiError %d", err, tt.wantStatus)
				}
				return
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTenants: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tenants = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveTenantsCachesProjects proves the ListProjects read is memoized:
// within the TTL a store outage is invisible because the cached list answers.
func TestResolveTenantsCachesProjects(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "team", Members: []string{"a"}})
	a := &API{provider: func() storage.Store { return f }}

	if _, err := a.resolveTenants(context.Background(), "team", nil); err != nil {
		t.Fatalf("warm-up resolve: %v", err)
	}
	f.ProjectsErr = errors.New("clickhouse down")
	got, err := a.resolveTenants(context.Background(), "team", nil)
	if err != nil {
		t.Fatalf("cached resolve during outage: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("tenants = %v, want [a]", got)
	}
}

// TestServicesResolvesAggregateTenants pins the wired handler's behavior at
// the HTTP level: /services keeps Tenant as the requested project and carries
// the resolved member set in Tenants (leaf requests stay [project], so the
// pre-aggregate behavior is unchanged).
func TestServicesResolvesAggregateTenants(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "team", Members: []string{"a", "b"}})
	mux := newMux(f)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
	req.Header.Set("X-Avuru-Tenant", "team")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := f.LastServiceQuery
	if q.Tenant != "team" || !reflect.DeepEqual(q.Tenants, []string{"a", "b"}) {
		t.Errorf("query = Tenant %q Tenants %v, want team / [a b]", q.Tenant, q.Tenants)
	}

	// A leaf project resolves to itself.
	rec2 := get(t, mux, "/api/v1/services")
	if rec2.Code != http.StatusOK {
		t.Fatalf("leaf status %d: %s", rec2.Code, rec2.Body.String())
	}
	q = f.LastServiceQuery
	if q.Tenant != storage.DefaultTenant || !reflect.DeepEqual(q.Tenants, []string{storage.DefaultTenant}) {
		t.Errorf("leaf query = Tenant %q Tenants %v, want default / [default]", q.Tenant, q.Tenants)
	}
}

// The end-to-end shape of P-3: with members set, a read handler queries the
// union instead of the aggregate id. Logs stand in for the 26 migrated read
// sites — they all take the same tenants slice from projectTenants.
func TestAggregateReadUnionsMembers(t *testing.T) {
	mux, c, f := adminMux(t)
	f.Projects = map[string]storage.Project{
		"estate": {ID: "estate", Label: "Estate", Members: []string{"prod-eu", "prod-us"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	req.Header.Set("X-Avuru-Tenant", "estate")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(f.LastLogQuery.Tenants, []string{"prod-eu", "prod-us"}) {
		t.Fatalf("query tenants = %v, want both members", f.LastLogQuery.Tenants)
	}
	if f.LastLogQuery.Tenant != "estate" {
		t.Fatalf("query tenant = %q, want the aggregate id kept for provenance", f.LastLogQuery.Tenant)
	}
}

// A leaf project — the overwhelmingly common case — must still resolve to
// exactly itself after the migration.
func TestLeafReadIsUnchanged(t *testing.T) {
	mux, c, f := adminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	req.Header.Set("X-Avuru-Tenant", "payments")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(f.LastLogQuery.Tenants, []string{"payments"}) {
		t.Fatalf("query tenants = %v, want [payments]", f.LastLogQuery.Tenants)
	}
}

// A viewer granted only one member sees only that member — the aggregate is a
// convenience over grants, never a way around them.
func TestAggregateReadIntersectsGrants(t *testing.T) {
	f := &storagetest.Fake{Projects: map[string]storage.Project{
		"estate": {ID: "estate", Members: []string{"prod-eu", "prod-us"}},
	}}
	a := &API{provider: func() storage.Store { return f }}

	got, err := a.resolveTenants(context.Background(), "estate", viewerOn("prod-eu"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"prod-eu"}) {
		t.Fatalf("tenants = %v, want [prod-eu]", got)
	}
}
