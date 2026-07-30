package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
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
