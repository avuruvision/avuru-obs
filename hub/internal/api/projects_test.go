package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// adminMuxCfg is adminMux with configurable Config.Projects (config-defined
// projects), for the reserved/read-only paths.
func adminMuxCfg(t *testing.T, projects []string) (*http.ServeMux, *http.Cookie, *storagetest.Fake) {
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
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc, Projects: projects})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}, f
}

func decodeProject(t *testing.T, w *httptest.ResponseRecorder) projectDTO {
	t.Helper()
	var p projectDTO
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

// The list is filtered per-identity, so a cache hit for a second identity on
// the same browser (sign-out, sign back in as someone else) would leak the
// first identity's project set — same rationale as the login/me no-store.
func TestProjectsNoStore(t *testing.T) {
	mux, cookie, _ := adminMux(t)
	w := doBody(mux, http.MethodGet, "/api/v1/projects", cookie, "")
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestProjectsMergeIncludesDBProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	f.Projects = map[string]storage.Project{
		"team-a": {ID: "team-a", Label: "Team A", Members: []string{}},
	}

	w := doBody(mux, http.MethodGet, "/api/v1/projects", cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Projects []projectDTO `json:"projects"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found bool
	for _, p := range resp.Projects {
		if p.ID == "team-a" {
			found = true
			if p.Source != "db" || !p.Editable || p.Label != "Team A" {
				t.Fatalf("team-a = %+v, want source=db editable=true label=Team A", p)
			}
		}
	}
	if !found {
		t.Fatalf("team-a missing from %+v", resp.Projects)
	}
}

func TestCreateProject(t *testing.T) {
	mux, cookie, f := adminMux(t)

	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"team-a","label":"Team A"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	p := decodeProject(t, w)
	if p.Source != "db" || !p.Editable || p.Label != "Team A" {
		t.Fatalf("dto = %+v", p)
	}
	if got := f.Projects["team-a"]; got.Label != "Team A" {
		t.Fatalf("fake missing project: %+v", f.Projects)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	mux, cookie, _ := adminMux(t)
	cases := []struct {
		name, body string
		want       int
	}{
		{"bad chars", `{"id":"Team_A","label":"x"}`, http.StatusBadRequest},
		{"leading digit", `{"id":"1team","label":"x"}`, http.StatusBadRequest},
		{"reserved default", `{"id":"default","label":"x"}`, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestCreateProjectDuplicate(t *testing.T) {
	mux, cookie, _ := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"team-a","label":"A"}`)
	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"team-a","label":"A2"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestCreateProjectConfigConflict(t *testing.T) {
	mux, cookie, _ := adminMuxCfg(t, []string{"prod"})
	w := doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"prod","label":"Prod"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestRenameProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"team-a","label":"A"}`)

	w := doBody(mux, http.MethodPut, "/api/v1/projects/team-a", cookie, `{"label":"Renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if f.Projects["team-a"].Label != "Renamed" {
		t.Fatalf("label = %q", f.Projects["team-a"].Label)
	}
}

func TestRenameProjectRejectsReadOnly(t *testing.T) {
	mux, cookie, _ := adminMuxCfg(t, []string{"prod"})
	for _, id := range []string{"default", "prod", "ghost"} { // reserved, config, unknown-db
		w := doBody(mux, http.MethodPut, "/api/v1/projects/"+id, cookie, `{"label":"x"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", id, w.Code)
		}
	}
}

func TestDeleteProject(t *testing.T) {
	mux, cookie, f := adminMux(t)
	doBody(mux, http.MethodPost, "/api/v1/projects", cookie, `{"id":"team-a","label":"A"}`)

	w := doBody(mux, http.MethodDelete, "/api/v1/projects/team-a", cookie, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if _, ok := f.Projects["team-a"]; ok {
		t.Fatalf("project not removed: %+v", f.Projects)
	}
}

func TestDeleteProjectRejectsReadOnly(t *testing.T) {
	mux, cookie, _ := adminMuxCfg(t, []string{"prod"})
	for _, id := range []string{"default", "prod", "ghost"} {
		w := doBody(mux, http.MethodDelete, "/api/v1/projects/"+id, cookie, "")
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", id, w.Code)
		}
	}
}

// TestProjectMutationsAdminOnly is defense in depth: an editor (non-admin) is
// rejected with 403 before any handler logic runs.
func TestProjectMutationsAdminOnly(t *testing.T) {
	mux, cookie := authedMux(t) // editor-on-payments identity
	create := authDo(mux, http.MethodPost, "/api/v1/projects", cookie, nil)
	if create.Code != http.StatusForbidden {
		t.Fatalf("POST status = %d, want 403", create.Code)
	}
	put := authDo(mux, http.MethodPut, "/api/v1/projects/x", cookie, nil)
	if put.Code != http.StatusForbidden {
		t.Fatalf("PUT status = %d, want 403", put.Code)
	}
	del := authDo(mux, http.MethodDelete, "/api/v1/projects/x", cookie, nil)
	if del.Code != http.StatusForbidden {
		t.Fatalf("DELETE status = %d, want 403", del.Code)
	}
}

// --- P-3: aggregate membership ---------------------------------------------

// dbProject seeds one live db project on the fake.
func dbProject(f *storagetest.Fake, id, label string, members ...string) {
	if f.Projects == nil {
		f.Projects = map[string]storage.Project{}
	}
	if members == nil {
		members = []string{}
	}
	f.Projects[id] = storage.Project{ID: id, Label: label, Members: members}
}

// Members are normalized on the way in: trimmed, deduped, sorted. The order is
// deterministic so the UI's checkbox list and the DTO never disagree.
func TestUpdateProjectSetsMembers(t *testing.T) {
	mux, c, f := adminMux(t)
	dbProject(f, "estate", "Estate")

	w := doBody(mux, "PUT", "/api/v1/projects/estate", c,
		`{"members":["prod-eu","prod-us","prod-eu","  ","prod-ap"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	got := decodeProject(t, w)
	want := []string{"prod-ap", "prod-eu", "prod-us"}
	if !reflect.DeepEqual(got.Members, want) {
		t.Fatalf("members = %v, want %v", got.Members, want)
	}
	if got.Label != "Estate" {
		t.Fatalf("label = %q, want Estate — a members-only PUT must not blank it", got.Label)
	}
	if saved := f.Projects["estate"]; !reflect.DeepEqual(saved.Members, want) {
		t.Fatalf("stored members = %v, want %v", saved.Members, want)
	}
}

// The label form and the members form are separate editors; each PUTs only its
// own field, so an omitted field must survive.
func TestUpdateProjectPartialKeepsTheOtherField(t *testing.T) {
	mux, c, f := adminMux(t)
	dbProject(f, "estate", "Estate", "prod-eu")

	w := doBody(mux, "PUT", "/api/v1/projects/estate", c, `{"label":"Whole estate"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("label PUT: %d body=%s", w.Code, w.Body)
	}
	if got := decodeProject(t, w); !reflect.DeepEqual(got.Members, []string{"prod-eu"}) {
		t.Fatalf("members = %v, want [prod-eu] preserved across a label edit", got.Members)
	}

	// An explicit empty array is the demotion back to a leaf.
	w = doBody(mux, "PUT", "/api/v1/projects/estate", c, `{"members":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("demote PUT: %d body=%s", w.Code, w.Body)
	}
	got := decodeProject(t, w)
	if len(got.Members) != 0 {
		t.Fatalf("members = %v, want empty", got.Members)
	}
	if got.Label != "Whole estate" {
		t.Fatalf("label = %q, want the rename kept", got.Label)
	}
}

func TestUpdateProjectMemberValidation(t *testing.T) {
	many := make([]string, 0, maxProjectMembers+1)
	for i := 0; i <= maxProjectMembers; i++ {
		many = append(many, fmt.Sprintf(`"p%d"`, i))
	}

	tests := []struct {
		name   string
		seed   func(*storagetest.Fake)
		body   string
		status int
		want   string
	}{
		{
			name:   "self",
			body:   `{"members":["estate","prod-eu"]}`,
			status: http.StatusBadRequest,
			want:   "cannot be a member of itself",
		},
		{
			name:   "malformed id",
			body:   `{"members":["Prod EU"]}`,
			status: http.StatusBadRequest,
			want:   "must match",
		},
		{
			name:   "too many",
			body:   `{"members":[` + strings.Join(many, ",") + `]}`,
			status: http.StatusBadRequest,
			want:   "at most",
		},
		{
			name:   "member is itself an aggregate",
			seed:   func(f *storagetest.Fake) { dbProject(f, "europe", "Europe", "prod-eu") },
			body:   `{"members":["europe"]}`,
			status: http.StatusConflict,
			want:   "one level deep",
		},
		{
			name:   "already a member elsewhere",
			seed:   func(f *storagetest.Fake) { dbProject(f, "world", "World", "estate") },
			body:   `{"members":["prod-eu"]}`,
			status: http.StatusConflict,
			want:   "already a member",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, c, f := adminMux(t)
			dbProject(f, "estate", "Estate")
			if tt.seed != nil {
				tt.seed(f)
			}
			w := doBody(mux, "PUT", "/api/v1/projects/estate", c, tt.body)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.status, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("body = %s, want it to mention %q", w.Body, tt.want)
			}
			if saved := f.Projects["estate"]; len(saved.Members) != 0 {
				t.Fatalf("rejected members were stored anyway: %v", saved.Members)
			}
		})
	}
}

// A member set that never existed as a db row is legitimate: a secondary
// cluster becomes a tenant the moment it ships its first span, and the
// aggregate should be wireable before that happens.
func TestUpdateProjectAcceptsUnknownMembers(t *testing.T) {
	mux, c, f := adminMux(t)
	dbProject(f, "estate", "Estate")

	w := doBody(mux, "PUT", "/api/v1/projects/estate", c, `{"members":["not-yet-seen"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
}

// The membership read feeds authorization, so it is memoized. A write that
// left the memo in place would keep resolving the old member set on this
// replica for up to tenantCacheTTL. Driven through the handler on a bare API
// value because that memo lives on the instance the router built.
func TestUpdateProjectInvalidatesTheMemo(t *testing.T) {
	f := &storagetest.Fake{}
	dbProject(f, "estate", "Estate")
	a := &API{provider: func() storage.Store { return f }, modules: modules.AllSet()}

	// Warm the memo: "estate" resolves to itself while it is still a leaf.
	if ts, err := a.resolveTenants(context.Background(), "estate", nil); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(ts, []string{"estate"}) {
		t.Fatalf("warm-up tenants = %v, want [estate]", ts)
	}

	req := httptest.NewRequest("PUT", "/api/v1/projects/estate", strings.NewReader(`{"members":["prod-eu"]}`))
	req.SetPathValue("id", "estate")
	w := httptest.NewRecorder()
	if err := a.handleUpdateProject(w, req); err != nil {
		t.Fatalf("update: %v", err)
	}

	ts, err := a.resolveTenants(context.Background(), "estate", nil)
	if err != nil {
		t.Fatalf("resolveTenants: %v", err)
	}
	if !reflect.DeepEqual(ts, []string{"prod-eu"}) {
		t.Fatalf("tenants = %v, want [prod-eu] immediately after the write", ts)
	}
}
