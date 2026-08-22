package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// projectIDRe: a tenant slug — lowercase alnum + hyphen, must start with a
// letter, ≤63 chars (fits a DNS label / the X-Avuru-Tenant header).
var projectIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

const maxProjectLabelLen = 200

// maxProjectMembers bounds an aggregate's fan-out: every member is one more id
// in the `Tenant IN (?)` of every query the project runs.
const maxProjectMembers = 32

type createProjectRequest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// updateProjectRequest is a PARTIAL update: an omitted field keeps its stored
// value. Both are pointers because the two editors are separate forms — the
// members form must not blank the label it never showed, and vice versa.
type updateProjectRequest struct {
	Label   *string   `json:"label"`
	Members *[]string `json:"members"`
}

func toProjectDTO(p storage.Project) projectDTO {
	return projectDTO{ID: p.ID, Label: p.Label, Source: "db", Editable: true, Members: p.Members}
}

func (a *API) isConfigProject(id string) bool {
	for _, p := range a.cfg.Projects {
		if p == id {
			return true
		}
	}
	return false
}

func identityUserID(ctx context.Context) string {
	if id := identityFrom(ctx); id != nil {
		return id.UserID
	}
	return ""
}

// projectDTO is one selectable project. Source records where it came from:
// "default" (always present), "config" (AVURUOBS_PROJECTS — Coroot's
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
	// The list is filtered per-identity below (filterProjectsForIdentity) —
	// like /auth/me, a cached copy would leak across a sign-out/sign-in as a
	// different identity on the same browser.
	w.Header().Set("Cache-Control", "no-store")
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

// handleCreateProject creates a UI-managed (db) project. A reserved id
// (default or a config project) or a duplicate live id is a 409, matching
// handleCreateUser's precedent for admin uniqueness conflicts.
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	var req createProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	id := strings.TrimSpace(req.ID)
	label := strings.TrimSpace(req.Label)
	if !projectIDRe.MatchString(id) {
		return badRequest("id must match ^[a-z][a-z0-9-]{0,62}$")
	}
	if len(label) > maxProjectLabelLen {
		return badRequest("label must be %d characters or fewer", maxProjectLabelLen)
	}
	if id == storage.DefaultTenant || a.isConfigProject(id) {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("%q is a reserved project", id)}
	}
	if _, err := st.GetProject(r.Context(), id); err == nil {
		return &apiError{status: http.StatusConflict, message: fmt.Sprintf("project %q already exists", id)}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	p := storage.Project{
		ID:        id,
		Label:     label,
		Members:   []string{},
		CreatedBy: identityUserID(r.Context()),
		CreatedAt: time.Now(),
	}
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	a.invalidateProjects()
	writeJSON(w, http.StatusCreated, toProjectDTO(p))
	return nil
}

// editableProject returns the live db project for id, or a 409 apiError when
// the id is deployment-managed (default/config) or has no live db row. Both
// rename and delete gate on this — only source "db" projects are mutable.
func (a *API) editableProject(ctx context.Context, st storage.Store, id string) (storage.Project, error) {
	if id == storage.DefaultTenant || a.isConfigProject(id) {
		return storage.Project{}, &apiError{status: http.StatusConflict,
			message: fmt.Sprintf("%q is a deployment-managed project and cannot be modified", id)}
	}
	p, err := st.GetProject(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Project{}, &apiError{status: http.StatusConflict,
			message: fmt.Sprintf("project %q is not editable", id)}
	}
	if err != nil {
		return storage.Project{}, err
	}
	return p, nil
}

// handleUpdateProject edits a db project's label and/or its members (the id is
// immutable). Setting members turns the project into an aggregate: its queries
// read the union of the members the caller may see (see resolveTenants).
func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	var req updateProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	p, err := a.editableProject(r.Context(), st, r.PathValue("id"))
	if err != nil {
		return err
	}
	if req.Label != nil {
		label := strings.TrimSpace(*req.Label)
		if len(label) > maxProjectLabelLen {
			return badRequest("label must be %d characters or fewer", maxProjectLabelLen)
		}
		p.Label = label
	}
	if req.Members != nil {
		members, err := a.validateMembers(r.Context(), st, p.ID, *req.Members)
		if err != nil {
			return err
		}
		p.Members = members
	}
	if err := st.SaveProject(r.Context(), p); err != nil {
		return err
	}
	// Membership feeds authorization; leaving the memo in place would keep this
	// replica resolving the OLD member set for up to tenantCacheTTL.
	a.invalidateProjects()
	writeJSON(w, http.StatusOK, toProjectDTO(p))
	return nil
}

// validateMembers normalizes a requested member set: trimmed, deduped, sorted,
// each a well-formed project id, never the project itself. Members need not
// exist yet — a secondary cluster is a tenant the moment it ships its first
// span, and an admin should be able to wire the aggregate before that.
//
// Membership is ONE level deep, enforced in both directions: a member may not
// itself be an aggregate, and a project that is already someone's member may
// not become one. resolveTenants expands exactly once, so a nested aggregate
// would silently read less than its tree — a wrong answer that looks like data.
func (a *API) validateMembers(ctx context.Context, st storage.Store, id string, in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !projectIDRe.MatchString(m) {
			return nil, badRequest("member %q must match ^[a-z][a-z0-9-]{0,62}$", m)
		}
		if m == id {
			return nil, badRequest("a project cannot be a member of itself")
		}
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Strings(out)
	if len(out) > maxProjectMembers {
		return nil, badRequest("at most %d members per project", maxProjectMembers)
	}
	if len(out) == 0 {
		return []string{}, nil // an explicit [] demotes an aggregate back to a leaf
	}
	all, err := st.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p.ID != id && len(p.Members) > 0 {
			if seen[p.ID] {
				return nil, &apiError{status: http.StatusConflict,
					message: fmt.Sprintf("project %q is itself an aggregate; membership is one level deep", p.ID)}
			}
			for _, m := range p.Members {
				if m == id {
					return nil, &apiError{status: http.StatusConflict,
						message: fmt.Sprintf("project %q is already a member of %q; membership is one level deep", id, p.ID)}
				}
			}
		}
	}
	return out, nil
}

// handleDeleteProject tombstones a db project (its telemetry ages out by TTL).
func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if _, err := a.editableProject(r.Context(), st, id); err != nil {
		return err
	}
	if err := st.DeleteProject(r.Context(), id); err != nil {
		return err
	}
	a.invalidateProjects()
	w.WriteHeader(http.StatusNoContent)
	return nil
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
