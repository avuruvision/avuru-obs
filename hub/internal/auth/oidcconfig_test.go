package auth

import "testing"

func TestParseOIDCConfig(t *testing.T) {
	yamlIn := `
issuer: https://kc.example.com/realms/avuru
clientId: avuru-obs
groupsClaim: groups
mapping:
  - {group: obs-admins, role: admin, projects: ["*"]}
  - {group: team-payments, role: editor, projects: [payments]}
defaultRole: viewer
defaultProjects: []
forceSSO: false
`
	c, err := ParseOIDCConfig([]byte(yamlIn), "supersecret")
	if err != nil {
		t.Fatal(err)
	}
	if c.Issuer != "https://kc.example.com/realms/avuru" || c.ClientID != "avuru-obs" {
		t.Fatalf("bad top-level: %+v", c)
	}
	if len(c.Mapping) != 2 || c.Mapping[0].Role != RoleAdmin {
		t.Fatalf("bad mapping: %+v", c.Mapping)
	}
	if c.DefaultRole != RoleViewer {
		t.Fatalf("defaultRole = %q", c.DefaultRole)
	}
	if c.ClientSecret != "supersecret" {
		t.Fatalf("secret not paired")
	}
}

func TestParseOIDCConfigRejectsBadRole(t *testing.T) {
	if _, err := ParseOIDCConfig([]byte("issuer: x\nclientId: y\nmapping:\n  - {group: g, role: superuser, projects: [\"*\"]}\n"), "s"); err == nil {
		t.Fatal("expected error on unknown role")
	}
}

func TestParseOIDCConfigRequiresIssuerAndClient(t *testing.T) {
	if _, err := ParseOIDCConfig([]byte("clientId: y\n"), "s"); err == nil {
		t.Fatal("missing issuer should error")
	}
}

// grantsEqual compares two grant sets ignoring order.
func grantsEqual(a, b []Grant) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[Grant]int, len(a))
	for _, g := range a {
		counts[g]++
	}
	for _, g := range b {
		counts[g]--
		if counts[g] < 0 {
			return false
		}
	}
	return true
}

func TestMapGroups(t *testing.T) {
	c := &OIDCConfig{
		Mapping: []GroupMap{
			{Group: "obs-admins", Role: RoleAdmin, Projects: []string{"*"}},
			{Group: "team-payments", Role: RoleEditor, Projects: []string{"payments"}},
		},
		DefaultRole: RoleViewer, DefaultProjects: []string{"public"},
	}
	if got := c.MapGroups([]string{"team-payments"}); !grantsEqual(got, []Grant{{Scope: "payments", Role: RoleEditor}}) {
		t.Fatalf("matched: %v", got)
	}
	if got := c.MapGroups([]string{"unknown"}); !grantsEqual(got, []Grant{{Scope: "public", Role: RoleViewer}}) {
		t.Fatalf("default: %v", got)
	}
	c.DefaultProjects = nil
	if got := c.MapGroups([]string{"unknown"}); len(got) != 0 {
		t.Fatalf("empty default: %v", got)
	}
}
