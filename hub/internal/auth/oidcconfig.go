package auth

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// GroupMap is one IdP-group → role-on-projects rule.
type GroupMap struct {
	Group    string
	Role     Role
	Projects []string
}

// OIDCConfig is the hot-reloadable OIDC configuration (auth.oidc in values).
// ClientSecret is injected from a mounted secret, never from the YAML body.
type OIDCConfig struct {
	Issuer          string
	ClientID        string
	ClientSecret    string
	GroupsClaim     string // default "groups"
	Mapping         []GroupMap
	DefaultRole     Role
	DefaultProjects []string
	ForceSSO        bool
}

type rawOIDC struct {
	Issuer      string `yaml:"issuer"`
	ClientID    string `yaml:"clientId"`
	GroupsClaim string `yaml:"groupsClaim"`
	Mapping     []struct {
		Group    string   `yaml:"group"`
		Role     string   `yaml:"role"`
		Projects []string `yaml:"projects"`
	} `yaml:"mapping"`
	DefaultRole     string   `yaml:"defaultRole"`
	DefaultProjects []string `yaml:"defaultProjects"`
	ForceSSO        bool     `yaml:"forceSSO"`
}

// ParseOIDCConfig validates the mounted YAML and pairs it with the secret.
func ParseOIDCConfig(body []byte, clientSecret string) (*OIDCConfig, error) {
	var r rawOIDC
	if err := yaml.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("oidc config: %w", err)
	}
	if r.Issuer == "" || r.ClientID == "" {
		return nil, fmt.Errorf("oidc config: issuer and clientId are required")
	}
	c := &OIDCConfig{
		Issuer: r.Issuer, ClientID: r.ClientID, ClientSecret: clientSecret,
		GroupsClaim: r.GroupsClaim, DefaultProjects: r.DefaultProjects, ForceSSO: r.ForceSSO,
	}
	if c.GroupsClaim == "" {
		c.GroupsClaim = "groups"
	}
	for i, m := range r.Mapping {
		role, ok := ParseRole(m.Role)
		if !ok {
			return nil, fmt.Errorf("oidc config: mapping[%d]: unknown role %q", i, m.Role)
		}
		if m.Group == "" {
			return nil, fmt.Errorf("oidc config: mapping[%d]: group is required", i)
		}
		c.Mapping = append(c.Mapping, GroupMap{Group: m.Group, Role: role, Projects: m.Projects})
	}
	if r.DefaultRole != "" {
		role, ok := ParseRole(r.DefaultRole)
		if !ok {
			return nil, fmt.Errorf("oidc config: unknown defaultRole %q", r.DefaultRole)
		}
		c.DefaultRole = role
	}
	return c, nil
}

// MapGroups turns a user's IdP groups into grants. Every project listed on a
// matched rule becomes one grant; when NO rule matches, the defaultRole is
// granted on defaultProjects (possibly none). Deduplicated by (scope, role).
func (c *OIDCConfig) MapGroups(groups []string) []Grant {
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
	for _, m := range c.Mapping {
		if inGroups[m.Group] {
			matched = true
			for _, p := range m.Projects {
				add(p, m.Role)
			}
		}
	}
	if !matched && c.DefaultRole != "" {
		for _, p := range c.DefaultProjects {
			add(p, c.DefaultRole)
		}
	}
	return out
}
