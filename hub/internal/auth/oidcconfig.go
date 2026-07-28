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
