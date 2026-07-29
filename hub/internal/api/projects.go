package api

import (
	"net/http"
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
