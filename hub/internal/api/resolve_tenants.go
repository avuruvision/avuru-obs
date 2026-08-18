package api

import (
	"context"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// projectCache memoizes ListProjects briefly for resolveTenants — same
// rationale as tenantCache: every telemetry request resolves its tenant set,
// and the FINAL-collapsed projects read shouldn't run on each one.
type projectCache struct {
	mu       sync.Mutex
	projects []storage.Project
	fetched  time.Time
}

// resolveTenants expands project into the tenant set its queries must read.
// A leaf project (no db row, or a db row with no members) reads itself. An
// aggregate (db row with members) reads its members intersected with the
// identity's granted projects — a nil identity (auth disabled) or a wildcard
// grant passes all members; an empty intersection is a 403, not an empty
// tenant set that would render as "no data". Store errors fail closed: an
// aggregate must never silently degrade to a leaf.
func (a *API) resolveTenants(ctx context.Context, project string, id *auth.Identity) ([]string, error) {
	ps, err := a.dbProjectsCached(ctx)
	if err != nil {
		return nil, err
	}
	var members []string
	for _, p := range ps {
		if p.ID == project {
			members = p.Members
			break
		}
	}
	if len(members) == 0 {
		return []string{project}, nil
	}
	if id == nil {
		return members, nil
	}
	out := make([]string, 0, len(members))
	for _, m := range members {
		// RoleFor covers "*" grants, so a wildcard identity keeps every member.
		if _, ok := id.RoleFor(m); ok {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, forbidden("no access to any member of project %q", project)
	}
	return out, nil
}

// dbProjectsCached returns the UI-managed projects, cached for
// tenantCacheTTL. Unlike dbProjects it propagates store errors: this feeds
// authorization (member expansion), where degrading to "no projects" would
// quietly turn an aggregate into a leaf.
func (a *API) dbProjectsCached(ctx context.Context) ([]storage.Project, error) {
	a.projects.mu.Lock()
	defer a.projects.mu.Unlock()
	if time.Since(a.projects.fetched) < tenantCacheTTL {
		return a.projects.projects, nil
	}
	st, err := a.store()
	if err != nil {
		return nil, err
	}
	ps, err := st.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	a.projects.projects, a.projects.fetched = ps, time.Now()
	return ps, nil
}
