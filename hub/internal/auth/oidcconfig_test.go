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
