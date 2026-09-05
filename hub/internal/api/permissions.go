package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
)

// The permissions matrix — what each role may actually do — read off the
// routing table as it is built, not written out a second time by hand. Add an
// admin-only POST anywhere and it shows up here; change a route's guard and
// this changes with it. A matrix that can disagree with the middleware is
// worse than no matrix, because it will be believed.

// routeGuard is one registered route's authorization, captured by routeIndex.
type routeGuard struct {
	Method    string
	Path      string
	MinRole   auth.Role
	AdminOnly bool
	Public    bool // registered with no session middleware at all
}

// routeIndex stands in for *http.ServeMux during registration: it records each
// route's guard, then delegates. Registering through it — rather than
// annotating sixty call sites — is what keeps the record honest, since there
// is no way to add a route that skips it.
type routeIndex struct {
	mux    *http.ServeMux
	routes []routeGuard
}

func newRouteIndex(mux *http.ServeMux) *routeIndex { return &routeIndex{mux: mux} }

func (ri *routeIndex) Handle(pattern string, h http.Handler) {
	ri.record(pattern, h)
	ri.mux.Handle(pattern, h)
}

func (ri *routeIndex) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	ri.record(pattern, http.HandlerFunc(h))
	ri.mux.HandleFunc(pattern, h)
}

func (ri *routeIndex) record(pattern string, h http.Handler) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		method, path = "GET", pattern
	}
	g := routeGuard{Method: method, Path: path, Public: true}
	if gr, isGuarded := h.(guarded); isGuarded {
		g.Public = false
		g.MinRole = gr.minRole
		g.AdminOnly = gr.adminOnly
	}
	ri.routes = append(ri.routes, g)
}

// areaMeta prettifies the path segment an area is derived from and files it
// under the part of the product it belongs to — the same division the sidebar
// uses, because a reader looking for "can an editor change alerts" looks where
// Alerts lives, not down an alphabetical list of twenty-six rows. Missing
// entries fall back to the raw segment under "Other", so a new area appears in
// the matrix under its own name rather than silently not appearing at all;
// TestEveryMatrixAreaIsDescribed fails the build before it can ship that way.
type areaMeta struct {
	label string
	group string
}

// Group names, most-watched first. The matrix renders in this order.
var areaGroupOrder = []string{
	groupTopology, groupSignals, groupOperations, groupInfrastructure, groupAdministration, groupOther,
}

const (
	groupTopology       = "Topology"
	groupSignals        = "Signals"
	groupOperations     = "Operations"
	groupInfrastructure = "Infrastructure"
	groupAdministration = "Administration"
	groupOther          = "Other"
)

var areaLabels = map[string]areaMeta{
	"services":       {"Services", groupTopology},
	"service-map":    {"Service map", groupTopology},
	"service-groups": {"Service groups", groupTopology},
	"health":         {"Service health", groupTopology},

	"traces":   {"Traces", groupSignals},
	"logs":     {"Logs", groupSignals},
	"metrics":  {"Metrics", groupSignals},
	"profiles": {"Profiling", groupSignals},
	"ai":       {"AI & LLM calls", groupSignals},
	"tags":     {"Tags & labels", groupSignals},

	"errors": {"Error tracking", groupOperations},
	"alerts": {"Alerting", groupOperations},
	"checks": {"Endpoint checks", groupOperations},

	"infra":   {"Nodes & pods", groupInfrastructure},
	"mesh":    {"Service mesh", groupInfrastructure},
	"network": {"Network health", groupInfrastructure},
	"green":   {"Energy & carbon", groupInfrastructure},
	"cost":    {"Cost & waste", groupInfrastructure},
	"agents":  {"Agents", groupInfrastructure},

	"projects":     {"Projects", groupAdministration},
	"users":        {"Users", groupAdministration},
	"collection":   {"Collection", groupAdministration},
	"capabilities": {"Modules", groupAdministration},
	"rates":        {"AI model rates", groupAdministration},
	"status":       {"Hub status", groupAdministration},
	"system":       {"System status", groupAdministration},
}

// areaAliases fold a path segment into the area it belongs to. /spans/{id} is
// the span-id lookup on the traces screen; a separate row for it would read as
// a distinct permission that does not exist.
var areaAliases = map[string]string{"spans": "traces"}

// permissionRole is one role and what it is for.
type permissionRoleDTO struct {
	Role        string `json:"role"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// permissionAreaDTO is one row of the matrix. Read/Write name the LOWEST role
// that can do it; empty means the area has no route of that kind at all (most
// signals are read-only, and saying so is more useful than a blank cell that
// could mean "nobody" or "everybody").
type permissionAreaDTO struct {
	Area  string `json:"area"`
	Label string `json:"label"`
	Group string `json:"group"`
	Read  string `json:"read,omitempty"`
	Write string `json:"write,omitempty"`
}

type permissionsResponse struct {
	Roles []permissionRoleDTO `json:"roles"`
	Areas []permissionAreaDTO `json:"areas"`
	// AuthEnabled is false on an install running without authentication, where
	// the matrix describes what WOULD apply — everyone is effectively an admin
	// until auth is switched on, and the screen has to say so.
	AuthEnabled bool `json:"authEnabled"`
}

var permissionRoles = []permissionRoleDTO{
	{Role: "admin", Label: "Admin",
		Description: "Everything a viewer and an editor can do, plus configuration: users, projects and ingest keys, alert channels, service groups, collection and the instance's storage status. Administration is global — an admin grant on a single project does not confer it."},
	{Role: "editor", Label: "Editor",
		Description: "Everything a viewer can do on their projects, plus the operational writes that are not configuration — today, triaging error issues."},
	{Role: "viewer", Label: "Viewer",
		Description: "Reads every signal for the projects they are granted, and nothing else. Grants are per project: a viewer on one project cannot see another."},
}

// rank orders roles from least to most privileged for "lowest role that can".
var roleRank = map[auth.Role]int{auth.RoleViewer: 1, auth.RoleEditor: 2, auth.RoleAdmin: 3}

// handlePermissions reports the roles and, per area of the product, the lowest
// role that can read it and the lowest that can change it — derived from the
// guards the routes registered with.
func (a *API) handlePermissions(w http.ResponseWriter, r *http.Request) error {
	type acc struct{ read, write auth.Role }
	byArea := map[string]*acc{}
	var order []string

	lowest := func(cur, next auth.Role) auth.Role {
		if cur == "" || roleRank[next] < roleRank[cur] {
			return next
		}
		return cur
	}

	for _, g := range a.routes {
		area, ok := areaOf(g.Path)
		if !ok {
			continue
		}
		role := g.MinRole
		if g.Public || role == "" {
			// Public routes (the login endpoints) and identity-only routes
			// (/auth/me, logout) are not a permission anyone is granted, so
			// they carry no floor. Skip rather than report "viewer".
			continue
		}
		e, seen := byArea[area]
		if !seen {
			e = &acc{}
			byArea[area] = e
			order = append(order, area)
		}
		if g.Method == http.MethodGet {
			e.read = lowest(e.read, role)
		} else {
			e.write = lowest(e.write, role)
		}
	}

	resp := permissionsResponse{
		Roles:       permissionRoles,
		Areas:       make([]permissionAreaDTO, 0, len(order)),
		AuthEnabled: a.cfg.Auth != nil,
	}
	for _, area := range order {
		e := byArea[area]
		meta, ok := areaLabels[area]
		if !ok {
			meta = areaMeta{label: area, group: groupOther}
		}
		resp.Areas = append(resp.Areas, permissionAreaDTO{
			Area: area, Label: meta.label, Group: meta.group,
			Read: string(e.read), Write: string(e.write),
		})
	}
	// Group first, alphabetical within it — a stable order the UI can render
	// section by section without re-sorting.
	sort.Slice(resp.Areas, func(i, j int) bool {
		gi, gj := groupRank(resp.Areas[i].Group), groupRank(resp.Areas[j].Group)
		if gi != gj {
			return gi < gj
		}
		return resp.Areas[i].Label < resp.Areas[j].Label
	})
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// groupRank orders a group by areaGroupOrder; anything unlisted sorts last.
func groupRank(group string) int {
	for i, g := range areaGroupOrder {
		if g == group {
			return i
		}
	}
	return len(areaGroupOrder)
}

// areaOf reduces "/api/v1/projects/{id}/keys" to "projects". Only the versioned
// API is described: /healthz and the OTLP ingest paths are not things a role is
// granted, and listing them would imply otherwise.
func areaOf(path string) (string, bool) {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	if seg == "" {
		return "", false
	}
	if alias, ok := areaAliases[seg]; ok {
		seg = alias
	}
	return seg, true
}
