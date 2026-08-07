package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// groupsMux is adminMux plus a chart-declared group set, so the tests can
// exercise both sides of the merge through the resolver the hub really uses.
func groupsMux(t *testing.T, declared ...health.Group) (*http.ServeMux, *http.Cookie, *storagetest.Fake) {
	t.Helper()
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := health.Default()
	cfg.Groups = declared
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth: svc,
		Groups: health.NewResolver(
			func() health.Config { return cfg },
			func() health.GroupStore { return f },
		),
	})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}, f
}

func listGroups(t *testing.T, mux *http.ServeMux, c *http.Cookie) []serviceGroupDTO {
	t.Helper()
	w := doBody(mux, http.MethodGet, "/api/v1/service-groups", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d body %s", w.Code, w.Body.String())
	}
	var resp serviceGroupsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Groups
}

func TestServiceGroupCreateListUpdateDelete(t *testing.T) {
	mux, c, f := groupsMux(t)

	w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c,
		`{"name":"payments","tier":"T0","namespaces":["pay"],"services":["checkout"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created serviceGroupDTO
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Source != "db" || !created.Editable {
		t.Fatalf("created = %+v, want source db and editable", created)
	}
	if f.ServiceGroups["payments"].CreatedBy == "" {
		t.Fatal("CreatedBy not recorded from the session identity")
	}

	groups := listGroups(t, mux, c)
	if len(groups) != 1 || groups[0].Name != "payments" || groups[0].Tier != "T0" {
		t.Fatalf("list = %+v", groups)
	}

	// Update keeps CreatedBy/CreatedAt: an edit is not a new group.
	createdAt := f.ServiceGroups["payments"].CreatedAt
	w = doBody(mux, http.MethodPut, "/api/v1/service-groups/payments", c,
		`{"tier":"T1","namespaces":["pay","billing"],"services":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d body %s", w.Code, w.Body.String())
	}
	got := f.ServiceGroups["payments"]
	if got.Tier != "T1" || len(got.Namespaces) != 2 || len(got.Services) != 0 {
		t.Fatalf("stored after update = %+v", got)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt changed on update: %v -> %v", createdAt, got.CreatedAt)
	}

	w = doBody(mux, http.MethodDelete, "/api/v1/service-groups/payments", c, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body %s", w.Code, w.Body.String())
	}
	if len(listGroups(t, mux, c)) != 0 {
		t.Fatal("group still listed after delete")
	}
}

// A chart-declared group is listed read-only and refuses every write. Offering
// an edit that the resolver would then override is the failure being avoided.
func TestServiceGroupConfigDeclaredIsReadOnly(t *testing.T) {
	declared := health.Group{Name: "core", Tier: health.TierT0, Selector: health.Selector{Namespaces: []string{"core"}}}
	mux, c, _ := groupsMux(t, declared)

	groups := listGroups(t, mux, c)
	if len(groups) != 1 || groups[0].Source != "config" || groups[0].Editable {
		t.Fatalf("list = %+v, want one read-only config row", groups)
	}

	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/service-groups", `{"name":"core","tier":"T1","namespaces":["x"]}`},
		{http.MethodPut, "/api/v1/service-groups/core", `{"tier":"T1","namespaces":["x"]}`},
		{http.MethodDelete, "/api/v1/service-groups/core", ""},
	}
	for _, tc := range cases {
		w := doBody(mux, tc.method, tc.path, c, tc.body)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s %s = %d, want 409 (body %s)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestServiceGroupDuplicateNameConflicts(t *testing.T) {
	mux, c, _ := groupsMux(t)
	body := `{"name":"payments","tier":"T0","namespaces":["pay"]}`
	if w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c, body); w.Code != http.StatusCreated {
		t.Fatalf("first create: %d", w.Code)
	}
	if w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c, body); w.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", w.Code)
	}
}

func TestServiceGroupUpdateUnknownIs404(t *testing.T) {
	mux, c, _ := groupsMux(t)
	w := doBody(mux, http.MethodPut, "/api/v1/service-groups/ghost", c, `{"tier":"T1","namespaces":["x"]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update unknown = %d, want 404", w.Code)
	}
	w = doBody(mux, http.MethodDelete, "/api/v1/service-groups/ghost", c, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", w.Code)
	}
}

// Writes run through health.Config.Validate — the same check the ConfigMap
// loader applies at boot — so the API cannot store a group the loader would
// reject on the next restart.
func TestServiceGroupValidation(t *testing.T) {
	mux, c, _ := groupsMux(t)
	cases := []struct{ name, body string }{
		{"unknown tier", `{"name":"g","tier":"T9","namespaces":["x"]}`},
		{"empty tier", `{"name":"g","namespaces":["x"]}`},
		{"empty selector", `{"name":"g","tier":"T1"}`},
		{"blank-only selector", `{"name":"g","tier":"T1","namespaces":["  "]}`},
		{"empty name", `{"name":"  ","tier":"T1","namespaces":["x"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("= %d, want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

// Selector entries are trimmed and de-duplicated: a stray blank line in the
// editor's textarea must not become a selector matching the empty namespace.
func TestServiceGroupSelectorIsCleaned(t *testing.T) {
	mux, c, f := groupsMux(t)
	w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c,
		`{"name":"g","tier":"T1","namespaces":[" pay ","", "pay","billing"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	got := f.ServiceGroups["g"].Namespaces
	if len(got) != 2 || got[0] != "pay" || got[1] != "billing" {
		t.Fatalf("namespaces = %v, want [pay billing]", got)
	}
}

// A stored group the config later takes the name of is flagged, not hidden:
// it stopped grouping anything, and an operator who never touched it deserves
// to see why.
func TestServiceGroupShadowedByConfig(t *testing.T) {
	mux, c, f := groupsMux(t, health.Group{
		Name: "payments", Tier: health.TierT0, Selector: health.Selector{Namespaces: []string{"pay"}},
	})
	f.ServiceGroups = map[string]storage.ServiceGroup{
		"payments": {Name: "payments", Tier: "T3", Namespaces: []string{"old"}},
	}
	var shadowed *serviceGroupDTO
	for _, g := range listGroups(t, mux, c) {
		if g.Source == "db" {
			shadowed = &g
		}
	}
	if shadowed == nil || !shadowed.Shadowed || !shadowed.Editable {
		t.Fatalf("db row = %+v, want shadowed and still editable (so it can be removed)", shadowed)
	}
}

// Writes are admin-only, like every other configuration surface; reads follow
// the health authorization so a viewer can see what the groups are.
func TestServiceGroupWritesAreAdminOnly(t *testing.T) {
	// The admin write path is covered above; here a viewer identity — the
	// anonymous one, the cheapest way to hold exactly one role — must be able
	// to read the definitions and unable to change them.
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth: svc,
		AnonymousIdentity: &auth.Identity{Name: "Anonymous", Anonymous: true,
			Grants: []auth.Grant{{Scope: "*", Role: auth.RoleViewer}}},
		Groups: health.NewResolver(health.Default, func() health.GroupStore { return f }),
	})

	if w := doBody(mux, http.MethodGet, "/api/v1/service-groups", nil, ""); w.Code != http.StatusOK {
		t.Fatalf("viewer read = %d, want 200", w.Code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/service-groups", `{"name":"g","tier":"T1","namespaces":["x"]}`},
		{http.MethodPut, "/api/v1/service-groups/g", `{"tier":"T1","namespaces":["x"]}`},
		{http.MethodDelete, "/api/v1/service-groups/g", ""},
	} {
		if w := doBody(mux, tc.method, tc.path, nil, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("viewer %s = %d, want 403 (body %s)", tc.method, w.Code, w.Body.String())
		}
	}
}

// The point of the whole feature: a group created here changes what /health
// reports, without a restart or a config edit.
func TestServiceGroupCreatedHereChangesHealth(t *testing.T) {
	mux, c, f := groupsMux(t)
	f.Services = []storage.ServiceStats{{Name: "checkout", SpanCount: 10}}
	f.Labels = []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}}

	if w := doBody(mux, http.MethodPost, "/api/v1/service-groups", c,
		`{"name":"payments","tier":"T0","services":["checkout"]}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}

	w := doBody(mux, http.MethodGet, "/api/v1/health/groups", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("health: %d body %s", w.Code, w.Body.String())
	}
	var resp healthGroupsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, g := range resp.Groups {
		if g.Name == "payments" && g.Tier == "T0" {
			return
		}
	}
	t.Fatalf("health groups = %+v, want checkout rolled up into the new T0 payments group", resp.Groups)
}
