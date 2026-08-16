package auth

import (
	"reflect"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestMergeMapping_ConfigOnly(t *testing.T) {
	cfg := []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}}
	got := MergeMapping(cfg, nil)
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.Group != "platform" || r.Role != RoleAdmin || r.Source != "config" || r.Editable || r.Shadowed {
		t.Errorf("config-only rule wrong: %+v", r)
	}
}

func TestMergeMapping_DBOnly(t *testing.T) {
	db := []storage.OIDCGroupMapping{{Group: "oncall", Role: "editor", Projects: []string{"default"}}}
	got := MergeMapping(nil, db)
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.Group != "oncall" || r.Role != RoleEditor || r.Source != "db" || !r.Editable || r.Shadowed {
		t.Errorf("db-only rule wrong: %+v", r)
	}
}

// TestMergeMapping_CollisionConfigWins is the central case: a group the
// chart declares AND an admin has authored in the DB. The config rule keeps
// applying, unmarked; the DB rule stays visible (still editable, so the
// admin can fix or delete it) but is marked Shadowed and, per
// TestGrantsFor_ShadowedRuleGrantsNothing below, contributes no grants.
func TestMergeMapping_CollisionConfigWins(t *testing.T) {
	cfg := []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}}
	db := []storage.OIDCGroupMapping{
		{Group: "platform", Role: "viewer", Projects: []string{"default"}},
		{Group: "oncall", Role: "editor", Projects: []string{"default"}},
	}
	got := MergeMapping(cfg, db)
	if len(got) != 3 {
		t.Fatalf("got %d rules, want 3: %+v", len(got), got)
	}
	byKey := map[string]EffectiveMapping{}
	for _, r := range got {
		byKey[r.Group+"|"+r.Source] = r
	}
	if r := byKey["platform|config"]; r.Editable || r.Shadowed {
		t.Errorf("config rule should be read-only and applying: %+v", r)
	}
	if r := byKey["platform|db"]; !r.Editable || !r.Shadowed {
		t.Errorf("db rule colliding with config must stay editable but be shadowed: %+v", r)
	}
	if r := byKey["oncall|db"]; !r.Editable || r.Shadowed {
		t.Errorf("db-only rule should be editable and applying: %+v", r)
	}
}

// TestMergeMapping_StableOrdering pins the sort so the UI list does not
// jitter between reads: (Group, Source) - and "config" sorts before "db" for
// the same group, so the winning rule always lists first.
func TestMergeMapping_StableOrdering(t *testing.T) {
	cfg := []GroupMap{
		{Group: "zzz-team", Role: RoleViewer, Projects: []string{"z"}},
		{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}},
	}
	db := []storage.OIDCGroupMapping{
		{Group: "oncall", Role: "editor", Projects: []string{"default"}},
		{Group: "platform", Role: "viewer", Projects: []string{"default"}},
	}
	got := MergeMapping(cfg, db)
	want := []string{"oncall|db", "platform|config", "platform|db", "zzz-team|config"}
	var gotKeys []string
	for _, r := range got {
		gotKeys = append(gotKeys, r.Group+"|"+r.Source)
	}
	if !reflect.DeepEqual(gotKeys, want) {
		t.Fatalf("order = %v, want %v", gotKeys, want)
	}
}

// TestMergeMapping_EmptyOverlayReproducesConfig is the "an install that
// never edits anything must behave byte-identically to today" guarantee. It
// compares GrantsFor, built from the merged (config-only) mapping, against
// OIDCConfig.MapGroups directly - including for a group nothing matches,
// where the defaultRole fallback kicks in. That fallback lives on
// OIDCConfig, outside the Mapping slice, so an empty overlay must not lose
// it.
func TestMergeMapping_EmptyOverlayReproducesConfig(t *testing.T) {
	cfg := &OIDCConfig{
		Mapping: []GroupMap{
			{Group: "obs-admins", Role: RoleAdmin, Projects: []string{"*"}},
			{Group: "team-payments", Role: RoleEditor, Projects: []string{"payments"}},
		},
		DefaultRole:     RoleViewer,
		DefaultProjects: []string{"public"},
	}
	merged := MergeMapping(cfg.Mapping, nil)
	if len(merged) != len(cfg.Mapping) {
		t.Fatalf("empty overlay changed rule count: %+v", merged)
	}
	for _, r := range merged {
		if r.Source != "config" || r.Editable || r.Shadowed {
			t.Fatalf("empty overlay must reproduce config rules verbatim: %+v", r)
		}
	}

	for _, groups := range [][]string{{"team-payments"}, {"unknown"}, {}} {
		got := GrantsFor(merged, groups, cfg.DefaultRole, cfg.DefaultProjects)
		want := cfg.MapGroups(groups)
		if !grantsEqual(got, want) {
			t.Errorf("groups %v: GrantsFor = %v, MapGroups = %v", groups, got, want)
		}
	}
}

// TestMergeMapping_DropsUnparseableDBRole pins the decision for a row the
// schema-less DB let through with a Role ParseRole rejects (hand-edited row,
// a retired role name, ...): MergeMapping drops it rather than defaulting it
// to some role, or to an empty one that could still compare equal to
// something downstream. It behaves as if the row were not there until an
// admin corrects or deletes it.
func TestMergeMapping_DropsUnparseableDBRole(t *testing.T) {
	db := []storage.OIDCGroupMapping{
		{Group: "broken", Role: "superuser", Projects: []string{"*"}},
		{Group: "oncall", Role: "editor", Projects: []string{"default"}},
	}
	got := MergeMapping(nil, db)
	if len(got) != 1 || got[0].Group != "oncall" {
		t.Fatalf("expected the unparseable row dropped, keeping only oncall: %+v", got)
	}
}

// TestGrantsFor_ShadowedRuleGrantsNothing proves the grant path specifically:
// GrantsFor is what feeds SetGroupMapper, so a shadowed DB rule must never
// leak a grant even though MergeMapping still returns it (for the UI).
func TestGrantsFor_ShadowedRuleGrantsNothing(t *testing.T) {
	cfg := []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}}
	db := []storage.OIDCGroupMapping{{Group: "platform", Role: "viewer", Projects: []string{"default"}}}
	grants := GrantsFor(MergeMapping(cfg, db), []string{"platform"}, "", nil)
	for _, g := range grants {
		if g.Role == RoleViewer && g.Scope == "default" {
			t.Fatalf("shadowed db rule leaked a grant: %+v", grants)
		}
	}
	if !grantsEqual(grants, []Grant{{Scope: "*", Role: RoleAdmin}}) {
		t.Fatalf("config rule's own grant is missing: %+v", grants)
	}
}
