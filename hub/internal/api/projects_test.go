package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// isStatus reports whether err is an *apiError with the given status.
func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == status
}

func TestHandleProjectsMergesDBSource(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "staging", Label: "Staging EU"})
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	if err := a.handleProjects(rec, req); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Projects []projectDTO `json:"projects"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	byID := map[string]projectDTO{}
	for _, p := range resp.Projects {
		byID[p.ID] = p
	}
	if d := byID["default"]; d.Source != "default" || d.Editable {
		t.Errorf("default = %+v, want source=default editable=false", d)
	}
	if p := byID["prod"]; p.Source != "config" || p.Editable {
		t.Errorf("prod = %+v, want source=config editable=false", p)
	}
	if s := byID["staging"]; s.Source != "db" || !s.Editable || s.Label != "Staging EU" {
		t.Errorf("staging = %+v, want source=db editable=true label=Staging EU", s)
	}
}

func TestCreateProject(t *testing.T) {
	f := &storagetest.Fake{}
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	// Happy path.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"staging","label":"Staging"}`))
	if err := a.handleCreateProject(rec, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := f.GetProject(context.Background(), "staging"); err != nil {
		t.Fatalf("not persisted: %v", err)
	}

	// Reserved id "default" → 400.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"default"}`))); !isStatus(err, 400) {
		t.Fatalf("default id: %v", err)
	}
	// Shadowing a config id → 409.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"prod"}`))); !isStatus(err, 409) {
		t.Fatalf("config shadow: %v", err)
	}
	// Invalid slug → 400.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"Has Space"}`))); !isStatus(err, 400) {
		t.Fatalf("bad slug: %v", err)
	}
	// Duplicate db id → 409.
	if err := a.handleCreateProject(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"staging"}`))); !isStatus(err, 409) {
		t.Fatalf("dup: %v", err)
	}
}

func TestRenameAndDeleteProject(t *testing.T) {
	f := &storagetest.Fake{}
	_ = f.SaveProject(context.Background(), storage.Project{ID: "staging", Label: "old"})
	a := &API{provider: func() storage.Store { return f }, cfg: Config{Projects: []string{"prod"}}}

	// Rename label.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/staging", strings.NewReader(`{"label":"new"}`))
	req.SetPathValue("id", "staging")
	if err := a.handleRenameProject(rec, req); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got, _ := f.GetProject(context.Background(), "staging"); got.Label != "new" {
		t.Fatalf("label = %q", got.Label)
	}
	// Renaming a config project → 409 (read-only).
	rc := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/prod", strings.NewReader(`{"label":"x"}`))
	rc.SetPathValue("id", "prod")
	if err := a.handleRenameProject(httptest.NewRecorder(), rc); !isStatus(err, 409) {
		t.Fatalf("rename config: %v", err)
	}
	// Delete.
	rd := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/staging", nil)
	rd.SetPathValue("id", "staging")
	if err := a.handleDeleteProject(httptest.NewRecorder(), rd); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.GetProject(context.Background(), "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("still present: %v", err)
	}
	// Deleting the default project → 409.
	rdd := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/default", nil)
	rdd.SetPathValue("id", "default")
	if err := a.handleDeleteProject(httptest.NewRecorder(), rdd); !isStatus(err, 409) {
		t.Fatalf("delete default: %v", err)
	}
}
