package auth

import "testing"

func TestRoleAllows(t *testing.T) {
	cases := []struct {
		have, need Role
		want       bool
	}{
		{RoleAdmin, RoleViewer, true},
		{RoleAdmin, RoleAdmin, true},
		{RoleEditor, RoleViewer, true},
		{RoleEditor, RoleAdmin, false},
		{RoleViewer, RoleEditor, false},
		{RoleViewer, RoleViewer, true},
	}
	for _, c := range cases {
		if got := c.have.Allows(c.need); got != c.want {
			t.Errorf("%s.Allows(%s) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}

func TestIdentityCanAccess(t *testing.T) {
	id := Identity{Grants: []Grant{
		{Scope: "payments", Role: RoleEditor},
		{Scope: "demo", Role: RoleViewer},
	}}
	global := Identity{Grants: []Grant{{Scope: "*", Role: RoleAdmin}}}

	if !id.CanAccess("payments", RoleEditor) {
		t.Error("editor on payments should access payments as editor")
	}
	if id.CanAccess("payments", RoleAdmin) {
		t.Error("editor must not act as admin")
	}
	if id.CanAccess("prod", RoleViewer) {
		t.Error("no grant on prod → no access")
	}
	if !global.CanAccess("anything", RoleAdmin) {
		t.Error("global admin accesses every project")
	}
	if id.IsAdmin() {
		t.Error("project editor is not a global admin")
	}
	if !global.IsAdmin() {
		t.Error("*-admin is global admin")
	}
	if got := id.ProjectScopes(); len(got) != 2 || got[0] != "payments" || got[1] != "demo" {
		t.Errorf("ProjectScopes = %v", got)
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "s3cret") {
		t.Error("correct password rejected")
	}
	if CheckPassword(h, "wrong") {
		t.Error("wrong password accepted")
	}
	if CheckPassword("", "anything") {
		t.Error("empty hash (SSO-only user) must never match")
	}
}

func TestParseRole(t *testing.T) {
	cases := []struct {
		input string
		want  Role
		ok    bool
	}{
		{"admin", RoleAdmin, true},
		{"editor", RoleEditor, true},
		{"viewer", RoleViewer, true},
		{"Admin", "", false},
		{"superuser", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ParseRole(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseRole(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestRoleForStrongestMatch(t *testing.T) {
	id := Identity{Grants: []Grant{
		{Scope: "*", Role: RoleViewer},
		{Scope: "payments", Role: RoleEditor},
	}}

	// payments has explicit editor grant, which is stronger than wildcard viewer
	r, ok := id.RoleFor("payments")
	if !ok || r != RoleEditor {
		t.Errorf("RoleFor(payments) = (%v, %v), want (editor, true)", r, ok)
	}

	// other project falls back to wildcard viewer
	r, ok = id.RoleFor("other")
	if !ok || r != RoleViewer {
		t.Errorf("RoleFor(other) = (%v, %v), want (viewer, true)", r, ok)
	}

	// corrupted grant on exclusive scope (no wildcard) should fail
	idNoWildcard := Identity{Grants: []Grant{
		{Scope: "prod", Role: Role("garbage")},
	}}
	r, ok = idNoWildcard.RoleFor("prod")
	if ok {
		t.Errorf("RoleFor(prod) with only corrupted role = (%v, %v), want (?, false)", r, ok)
	}

	if idNoWildcard.CanAccess("prod", RoleViewer) {
		t.Error("corrupted grant must not grant access")
	}
}

func TestAllowsUnknownNeedFailsClosed(t *testing.T) {
	if RoleAdmin.Allows(Role("garbage")) {
		t.Error("admin must not satisfy unknown role requirement")
	}
	if RoleViewer.Allows(Role("unknown")) {
		t.Error("viewer must not satisfy unknown role requirement")
	}
}

func TestHasWildcard(t *testing.T) {
	// global admin has wildcard
	global := Identity{Grants: []Grant{{Scope: "*", Role: RoleAdmin}}}
	if !global.HasWildcard() {
		t.Error("global admin should have wildcard")
	}

	// project-only identity has no wildcard
	project := Identity{Grants: []Grant{{Scope: "payments", Role: RoleEditor}}}
	if project.HasWildcard() {
		t.Error("project-only identity should not have wildcard")
	}

	// corrupted wildcard grant (invalid role) is not a wildcard
	corrupted := Identity{Grants: []Grant{{Scope: "*", Role: Role("garbage")}}}
	if corrupted.HasWildcard() {
		t.Error("corrupted wildcard grant must not count as wildcard")
	}
}

func TestProjectScopesDedupes(t *testing.T) {
	id := Identity{Grants: []Grant{
		{Scope: "payments", Role: RoleViewer},
		{Scope: "demo", Role: RoleEditor},
		{Scope: "payments", Role: RoleEditor}, // duplicate payments
		{Scope: "*", Role: RoleAdmin},         // wildcard excluded
	}}

	got := id.ProjectScopes()
	if len(got) != 2 || got[0] != "payments" || got[1] != "demo" {
		t.Errorf("ProjectScopes = %v, want [payments demo]", got)
	}
}
