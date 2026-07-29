package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

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
	json.NewDecoder(rec.Body).Decode(&resp)

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
