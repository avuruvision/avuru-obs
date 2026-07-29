package api

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// projectDTO is one selectable project. Source records where it came from:
// "default" (always present), "config" (AVURUOPS_PROJECTS — Coroot's
// "defined through the config" mode), "db" (UI-managed; rename/delete allowed),
// "data" (tenant observed in telemetry; auto-discovered), or "granted" (an
// RBAC-granted scope with no config/data entry yet — see
// filterProjectsForIdentity). Label/Members are set only for db projects (or a
// db row shadowing a config id, which stays read-only). Editable is true only
// for source=="db".
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

// handleProjects returns {default} ∪ config ∪ db ∪ observed-in-data, filtered
// to the caller's scopes. db projects (source "db") carry a label and are
// editable; default/config are read-only. It deliberately answers 200 with the
// default+config list when ClickHouse is down — the project switcher must
// always render. Precedence (first source wins per id):
// default > config > db > data > granted.
func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) error {
	dtos := map[string]*projectDTO{}
	var order []string
	add := func(id, source string) *projectDTO {
		if d, ok := dtos[id]; ok {
			return d
		}
		d := &projectDTO{ID: id, Source: source}
		dtos[id] = d
		order = append(order, id)
		return d
	}

	add(storage.DefaultTenant, "default")
	for _, p := range a.cfg.Projects {
		if p != "" && p != storage.DefaultTenant {
			add(p, "config")
		}
	}
	// db rows: a new id becomes source "db" (editable); a db row for an existing
	// default/config id only adds a label and stays read-only (config wins).
	for _, p := range a.dbProjects(r) {
		d := add(p.ID, "db")
		d.Label = p.Label
		d.Members = p.Members
		if d.Source == "db" {
			d.Editable = true
		}
	}
	for _, t := range a.observedTenants(r) {
		add(t, "data")
	}

	resp := projectsResponse{Projects: make([]projectDTO, 0, len(order))}
	for _, id := range order {
		resp.Projects = append(resp.Projects, *dtos[id])
	}
	sort.Slice(resp.Projects, func(i, j int) bool { return resp.Projects[i].ID < resp.Projects[j].ID })
	resp.Projects = filterProjectsForIdentity(resp.Projects, identityFrom(r.Context()))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// dbProjects returns UI-managed projects, or nil on any store error — the
// handler still answers 200 with default+config so the switcher renders.
func (a *API) dbProjects(r *http.Request) []storage.Project {
	st, err := a.store()
	if err != nil {
		return nil
	}
	ps, err := st.ListProjects(r.Context())
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
