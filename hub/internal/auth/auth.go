// Package auth owns identity, roles, sessions and login providers — the
// enterprise seam promised by agent_docs/architecture.md. Handlers consume
// only Identity; storage is the storage.Store auth methods.
package auth

// Role is one of the three fixed roles (Coroot/Grafana parity — custom roles
// stay an enterprise-seam extension, see the AEP).
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// rank orders roles for Allows. Unknown roles rank below viewer.
func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// Allows reports whether holding r satisfies a requirement of need.
func (r Role) Allows(need Role) bool {
	if need.rank() == 0 {
		return false
	}
	return r.rank() >= need.rank()
}

// ParseRole validates a stored/user-supplied role string.
func ParseRole(s string) (Role, bool) {
	switch Role(s) {
	case RoleAdmin, RoleEditor, RoleViewer:
		return Role(s), true
	}
	return "", false
}

// Grant is role-on-scope. Scope "*" means every project.
type Grant struct {
	Scope string `json:"scope"`
	Role  Role   `json:"role"`
}

// Identity is the authenticated caller: a user, or the synthetic anonymous
// identity when anonymous mode is enabled.
type Identity struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	// Origin mirrors AuthUser.Origin ("local" | "oidc"); empty for the
	// anonymous identity. Emitted by /auth/me so the SPA can SHOW how the user
	// signed in. It is not the test for "does a password form apply" — the demo
	// viewer is a local account that still cannot rotate its own credential, so
	// /auth/me carries a separate passwordChange field for that (api's
	// passwordChangeFor).
	Origin    string  `json:"origin"`
	Anonymous bool    `json:"anonymous"`
	Grants    []Grant `json:"grants"`
}

// RoleFor resolves the strongest role the identity holds on project ("*"
// grants apply everywhere). ok=false when no grant covers the project.
func (id Identity) RoleFor(project string) (Role, bool) {
	var best Role
	found := false
	for _, g := range id.Grants {
		if g.Scope == "*" || g.Scope == project {
			if _, ok := ParseRole(string(g.Role)); !ok {
				continue
			}
			if !found || g.Role.rank() > best.rank() {
				best, found = g.Role, true
			}
		}
	}
	return best, found
}

// CanAccess reports whether the identity may act on project at min role.
func (id Identity) CanAccess(project string, min Role) bool {
	r, ok := id.RoleFor(project)
	return ok && r.Allows(min)
}

// IsAdmin reports global admin (an admin grant on "*").
func (id Identity) IsAdmin() bool {
	for _, g := range id.Grants {
		if g.Scope == "*" && g.Role == RoleAdmin {
			return true
		}
	}
	return false
}

// HasWildcard reports whether any grant applies to every project ("*" scope,
// any valid role). Pair with ProjectScopes: a wildcard identity may have an
// empty explicit scope list yet see all projects.
func (id Identity) HasWildcard() bool {
	for _, g := range id.Grants {
		if g.Scope == "*" {
			if _, ok := ParseRole(string(g.Role)); ok {
				return true
			}
		}
	}
	return false
}

// ProjectScopes lists the explicitly granted projects (excluding "*"),
// in grant order, deduplicating repeats (use HasWildcard to detect global
// access via "*" scope).
func (id Identity) ProjectScopes() []string {
	var out []string
	seen := make(map[string]bool)
	for _, g := range id.Grants {
		if g.Scope != "*" && !seen[g.Scope] {
			out = append(out, g.Scope)
			seen[g.Scope] = true
		}
	}
	return out
}
