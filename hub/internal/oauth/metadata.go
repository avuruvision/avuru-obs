package oauth

// The two discovery documents an MCP client fetches, built from one public URL
// so they cannot disagree about where this server lives.

// ProtectedResource is RFC 9728 protected-resource metadata: what the /mcp
// endpoint is and which authorization server guards it.
type ProtectedResource struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
}

// AuthorizationServer is RFC 8414 authorization-server metadata.
type AuthorizationServer struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

// Endpoint paths. They sit under /api/v1 with the rest of the API — metadata
// advertises absolute URLs, so the path is free — while the two .well-known
// documents must be at the origin root, which is why /mcp could not live under
// /api either.
const (
	PathAuthorize   = "/api/v1/auth/oauth/authorize"
	PathToken       = "/api/v1/auth/oauth/token"
	PathRegister    = "/api/v1/auth/oauth/register"
	PathRevoke      = "/api/v1/auth/oauth/revoke"
	PathConsent     = "/api/v1/auth/oauth/consent"
	PathWellKnownAS = "/.well-known/oauth-authorization-server"
	PathWellKnownPR = "/.well-known/oauth-protected-resource"
	// RFC 9728 §3.1 inserts the resource's path component into the well-known
	// URL. Clients differ on which form they try, so the hub serves both.
	PathWellKnownPRMCP = "/.well-known/oauth-protected-resource/mcp"
)

// NewProtectedResource builds the RFC 9728 document.
func NewProtectedResource(publicURL string) ProtectedResource {
	return ProtectedResource{
		Resource:               ResourceURI(publicURL),
		AuthorizationServers:   []string{Issuer(publicURL)},
		ScopesSupported:        []string{ScopeMCPRead},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Avuru Obs",
	}
}

// NewAuthorizationServer builds the RFC 8414 document.
//
// `code_challenge_methods_supported` is exactly ["S256"] — see
// ChallengeMethodS256 for why `plain` is absent. There is deliberately no
// openid-configuration document: this server mints no id_token, and publishing
// one would invite clients to validate something that does not exist.
func NewAuthorizationServer(publicURL string, dynamicRegistration bool) AuthorizationServer {
	base := Issuer(publicURL)
	m := AuthorizationServer{
		Issuer:                        base,
		AuthorizationEndpoint:         base + PathAuthorize,
		TokenEndpoint:                 base + PathToken,
		RevocationEndpoint:            base + PathRevoke,
		ResponseTypesSupported:        []string{"code"},
		GrantTypesSupported:           []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported: []string{ChallengeMethodS256},
		// Public clients only: a hosted client that registers dynamically has
		// nowhere to keep a secret, and PKCE is what protects the exchange.
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   []string{ScopeMCPRead, ScopeOfflineAccess},
	}
	if dynamicRegistration {
		m.RegistrationEndpoint = base + PathRegister
	}
	return m
}
