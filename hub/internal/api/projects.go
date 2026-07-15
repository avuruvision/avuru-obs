package api

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// projectDTO is one selectable project. Source records where it came from:
// "default" (always present), "config" (AVURUOPS_PROJECTS — Coroot's
// "defined through the config" mode), or "data" (tenant observed in
// telemetry; auto-discovered).
type projectDTO struct {
	ID     string `json:"id"`
	Source string `json:"source"`
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

// handleProjects returns {default} ∪ configured ∪ observed-in-data. It
// deliberately answers 200 with the config list when ClickHouse is down —
// the project switcher must always render.
func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) error {
	set := map[string]string{storage.DefaultTenant: "default"}
	for _, p := range a.cfg.Projects {
		if p != "" && p != storage.DefaultTenant {
			set[p] = "config"
		}
	}
	for _, t := range a.observedTenants(r) {
		if _, ok := set[t]; !ok {
			set[t] = "data"
		}
	}

	resp := projectsResponse{Projects: make([]projectDTO, 0, len(set))}
	for id, source := range set {
		resp.Projects = append(resp.Projects, projectDTO{ID: id, Source: source})
	}
	sort.Slice(resp.Projects, func(i, j int) bool { return resp.Projects[i].ID < resp.Projects[j].ID })
	writeJSON(w, http.StatusOK, resp)
	return nil
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
