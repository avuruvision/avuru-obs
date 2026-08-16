package auth

import (
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// EffectiveMapping is one merged rule plus where it came from, so the UI can
// render chart-declared rules read-only and mark a shadowed one.
type EffectiveMapping struct {
	Group    string
	Role     Role
	Projects []string
	Source   string // "config" | "db"
	Editable bool   // db rules only
	Shadowed bool   // a db rule whose group the config also declares
}

// MergeMapping layers UI-authored DB rules over the chart-declared config
// rules, the same way service_group's resolver layers stored groups over
// declared ones: config always wins a group-name collision. A DB row whose
// group the config also declares is kept - so the UI can show it and explain
// why it stopped applying - but marked Shadowed; GrantsFor excludes shadowed
// rules when deriving grants.
//
// A DB row whose Role fails ParseRole is dropped from the result entirely.
// The DB is not schema-enforced, so a hand-edited or since-retired role
// string must not silently resolve to some other role, or to an empty one
// that could still compare equal to something downstream - the row is
// treated as if it were not there until an admin corrects or deletes it.
//
// The result is sorted by (Group, Source) so the UI list is stable across
// reads. "config" sorts before "db" lexically, which conveniently puts the
// winning rule first when both exist for a group.
func MergeMapping(cfg []GroupMap, db []storage.OIDCGroupMapping) []EffectiveMapping {
	declared := make(map[string]bool, len(cfg))
	out := make([]EffectiveMapping, 0, len(cfg)+len(db))
	for _, m := range cfg {
		declared[m.Group] = true
		out = append(out, EffectiveMapping{
			Group:    m.Group,
			Role:     m.Role,
			Projects: m.Projects,
			Source:   "config",
		})
	}
	for _, m := range db {
		role, ok := ParseRole(m.Role)
		if !ok {
			continue
		}
		out = append(out, EffectiveMapping{
			Group:    m.Group,
			Role:     role,
			Projects: m.Projects,
			Source:   "db",
			Editable: true,
			Shadowed: declared[m.Group],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// GrantsFor turns a user's IdP groups into grants from the merged mapping.
// It is the function installed via SetGroupMapper once defaultRole /
// defaultProjects are bound in from the live OIDCConfig (cmd/hub/oidc.go),
// so overlay rules take effect at read time without touching identity
// resolution.
//
// This mirrors OIDCConfig.MapGroups exactly, including its de-duplication
// via a seen map and its defaultRole fallback for a group nothing matched -
// that fallback must survive an empty overlay unchanged (see
// TestMergeMapping_EmptyOverlayReproducesConfig). A shadowed rule is
// skipped: it never counts as a match and never contributes a grant, even
// though MergeMapping still returns it for the UI to show.
func GrantsFor(mapping []EffectiveMapping, groups []string, defaultRole Role, defaultProjects []string) []Grant {
	inGroups := make(map[string]bool, len(groups))
	for _, g := range groups {
		inGroups[g] = true
	}
	var out []Grant
	seen := make(map[string]bool)
	add := func(scope string, role Role) {
		k := scope + "|" + string(role)
		if !seen[k] {
			seen[k] = true
			out = append(out, Grant{Scope: scope, Role: role})
		}
	}
	matched := false
	for _, m := range mapping {
		if m.Shadowed || !inGroups[m.Group] {
			continue
		}
		matched = true
		for _, p := range m.Projects {
			add(p, m.Role)
		}
	}
	if !matched && defaultRole != "" {
		for _, p := range defaultProjects {
			add(p, defaultRole)
		}
	}
	return out
}
