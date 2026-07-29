package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// projectDTO is one selectable project. Source records provenance: "default"
// (always present), "config" (AVURUOPS_PROJECTS — Coroot's "defined through the
// config" mode), "db" (UI-managed), "data" (tenant observed in telemetry;
// auto-discovered), or "granted" (an RBAC-granted scope with no other entry yet
// — see filterProjectsForIdentity). Editable is true only for db projects —
// default/config are deployment-owned and read-only; data/granted are not real
// project records. Label and Members are carried for db projects.
type projectDTO struct {
	ID       string   `json:"id"`
	Label    string   `json:"label,omitempty"`
	Source   string   `json:"source"`
	Editable bool     `json:"editable"`
	Members  []string `json:"members,omitempty"`
}

type projectsResponse struct {
	Projects []projectDTO `json:"projects"`
}

// tenantCache memoizes ListTenants briefly: the switcher polls, DISTINCT
// over two tables shouldn't run on every render.
type tenantCache struct {
	mu      sync.Mutex
	tenants []string
	fetched time.Time
}

const tenantCacheTTL = 30 * time.Second

// handleProjects returns {default} ∪ config ∪ db ∪ observed-in-data, filtered to
// the caller's scopes. Fixed precedence when an id appears from several origins:
// default > config > db > data > granted. Answers 200 with default+config even
// when ClickHouse is down — the project switcher must always render.
func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) error {
	byID := map[string]projectDTO{
		storage.DefaultTenant: {ID: storage.DefaultTenant, Source: "default"},
	}
	for _, p := range a.cfg.Projects {
		if p != "" && p != storage.DefaultTenant {
			if _, ok := byID[p]; !ok {
				byID[p] = projectDTO{ID: p, Source: "config"}
			}
		}
	}
	for _, p := range a.dbProjects(r) {
		if _, ok := byID[p.ID]; !ok {
			byID[p.ID] = projectDTO{
				ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members,
			}
		}
	}
	for _, t := range a.observedTenants(r) {
		if _, ok := byID[t]; !ok {
			byID[t] = projectDTO{ID: t, Source: "data"}
		}
	}

	resp := projectsResponse{Projects: make([]projectDTO, 0, len(byID))}
	for _, p := range byID {
		resp.Projects = append(resp.Projects, p)
	}
	sort.Slice(resp.Projects, func(i, j int) bool { return resp.Projects[i].ID < resp.Projects[j].ID })
	resp.Projects = filterProjectsForIdentity(resp.Projects, identityFrom(r.Context()))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// dbProjects returns UI-managed projects, or nil when the store is unreachable
// (degrade like observedTenants — the list must always render).
func (a *API) dbProjects(r *http.Request) []storage.Project {
	s := a.provider()
	if s == nil {
		return nil
	}
	ps, err := s.ListProjects(r.Context())
	if err != nil {
		return nil
	}
	return ps
}

// filterProjectsForIdentity restricts the merged (config+observed) project
// list to what the identity may see. A wildcard identity ("*" scope grant) —
// and a nil identity, meaning auth is disabled — passes through unfiltered.
// Otherwise the response is intersected with the identity's granted scopes,
// AND any granted scope missing from the merged list is appended: a project
// with a grant but no config entry or observed data yet must still appear so
// the switcher can select it.
func filterProjectsForIdentity(projects []projectDTO, id *auth.Identity) []projectDTO {
	if id == nil || id.HasWildcard() {
		return projects
	}
	scopes := id.ProjectScopes()
	allowed := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		allowed[s] = true
	}
	out := make([]projectDTO, 0, len(scopes))
	seen := make(map[string]bool, len(scopes))
	for _, p := range projects {
		if allowed[p.ID] {
			out = append(out, p)
			seen[p.ID] = true
		}
	}
	for _, s := range scopes {
		if !seen[s] {
			out = append(out, projectDTO{ID: s, Source: "granted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// projectIDRe bounds a project id: a lowercase slug, so it is a safe tenant
// value and a clean URL/path segment.
var projectIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type createProjectRequest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type renameProjectRequest struct {
	Label string `json:"label"`
}

// reservedProject reports whether id is deployment-owned (the built-in default
// or a config-declared project) and therefore not editable/deletable via the UI.
func (a *API) reservedProject(id string) bool {
	return id == storage.DefaultTenant || slices.Contains(a.cfg.Projects, id)
}

// handleCreateProject creates a UI-managed project. A reserved id is 400
// (default) or 409 (config-shadow); a duplicate db id is 409; a bad slug is 400.
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	var req createProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.ID == storage.DefaultTenant {
		return badRequest("%q is reserved", storage.DefaultTenant)
	}
	if !projectIDRe.MatchString(req.ID) {
		return badRequest("invalid project id %q (lowercase letters, digits, hyphens; must start with a letter)", req.ID)
	}
	if slices.Contains(a.cfg.Projects, req.ID) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is defined through config and cannot be created here", req.ID)}
	}
	if _, err := st.GetProject(r.Context(), req.ID); err == nil {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q already exists", req.ID)}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	p := storage.Project{ID: req.ID, Label: req.Label, CreatedBy: creatorOf(r)}
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true})
	return nil
}

// handleRenameProject edits a db project's label. Reserved (default/config)
// projects are read-only → 409; an unknown id is 404.
func (a *API) handleRenameProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if a.reservedProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is deployment-owned and cannot be modified here", id)}
	}
	var req renameProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	p, err := st.GetProject(r.Context(), id)
	if err != nil {
		return err // ErrNotFound -> 404
	}
	p.Label = req.Label
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members})
	return nil
}

// handleDeleteProject tombstones a db project. Reserved projects → 409; unknown
// → 404. Telemetry is untouched (ages out by TTL; a still-active tenant
// re-appears as source=data — intended).
func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if a.reservedProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q is deployment-owned and cannot be deleted here", id)}
	}
	if err := st.DeleteProject(r.Context(), id); err != nil {
		return err // ErrNotFound -> 404
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// creatorOf returns the acting admin's id for the audit column, or "" when auth
// is disabled.
func creatorOf(r *http.Request) string {
	if id := identityFrom(r.Context()); id != nil {
		return id.UserID
	}
	return ""
}

// observedTenants returns recently-seen tenants, cached for tenantCacheTTL.
// Store outages degrade to the empty list (never an error).
func (a *API) observedTenants(r *http.Request) []string {
	s := a.provider()
	if s == nil {
		return nil
	}
	a.tenants.mu.Lock()
	defer a.tenants.mu.Unlock()
	if time.Since(a.tenants.fetched) < tenantCacheTTL {
		return a.tenants.tenants
	}
	ts, err := s.ListTenants(r.Context())
	if err != nil {
		return a.tenants.tenants // stale beats missing; store may be flaky
	}
	a.tenants.tenants, a.tenants.fetched = ts, time.Now()
	return ts
}
