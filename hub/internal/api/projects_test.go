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
