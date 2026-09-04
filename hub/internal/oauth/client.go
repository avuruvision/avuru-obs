package oauth

import "strings"

// Registration is the RFC 7591 request body a client sends to register itself.
type Registration struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
}

// RegistrationResponse is what RFC 7591 returns. No client_secret: every client
// here is public and proves itself with PKCE, because a hosted client that
// registered itself has nowhere to keep a secret.
type RegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope"`
}

// Registration bounds. Registration is unauthenticated by necessity — a client
// that has never met this server has nothing to authenticate with — so the
// limits are what stop it being a free write endpoint. It still grants NOTHING:
// a registered client can do nothing at all until a person consents.
const (
	MaxRedirectURIs   = 5
	MaxClientNameLen  = 120
	MaxRegistrationID = 64
)

// ValidateRegistration checks a registration request and returns the values to
// store, or a protocol error naming what was wrong.
//
// Everything a client declares about itself — name, logo, homepage — is
// unverified and stays that way. The consent screen presents it as unverified
// and shows the redirect host beside it, because the host is the one fact a
// person can actually check.
func ValidateRegistration(r Registration) (Registration, *Error) {
	name := strings.TrimSpace(r.ClientName)
	if name == "" {
		return Registration{}, &Error{Code: ErrInvalidClientMetadata, Description: "client_name is required"}
	}
	if len(name) > MaxClientNameLen {
		return Registration{}, &Error{Code: ErrInvalidClientMetadata, Description: "client_name is too long"}
	}
	if len(r.RedirectURIs) == 0 {
		return Registration{}, &Error{Code: ErrInvalidRedirectURI, Description: "at least one redirect_uri is required"}
	}
	if len(r.RedirectURIs) > MaxRedirectURIs {
		return Registration{}, &Error{Code: ErrInvalidRedirectURI, Description: "too many redirect_uris"}
	}
	for _, u := range r.RedirectURIs {
		if !ValidRedirectURI(u) {
			return Registration{}, &Error{
				Code:        ErrInvalidRedirectURI,
				Description: "redirect_uri must be an absolute https URL with no fragment or userinfo",
			}
		}
	}
	// Only the public-client shape is issued. A confidential client would need
	// a secret this server has no way to deliver to a dynamic registrant.
	if m := r.TokenEndpointAuthMethod; m != "" && m != "none" {
		return Registration{}, &Error{
			Code:        ErrInvalidClientMetadata,
			Description: "only token_endpoint_auth_method=none is supported; clients prove themselves with PKCE",
		}
	}
	for _, g := range r.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return Registration{}, &Error{
				Code:        ErrInvalidClientMetadata,
				Description: "unsupported grant_type: " + g,
			}
		}
	}
	for _, t := range r.ResponseTypes {
		if t != "code" {
			return Registration{}, &Error{
				Code:        ErrInvalidClientMetadata,
				Description: "unsupported response_type: " + t,
			}
		}
	}

	out := Registration{
		ClientName:              name,
		RedirectURIs:            r.RedirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   NormalizeScope(r.Scope),
		SoftwareID:              trunc(r.SoftwareID, MaxRegistrationID),
		ClientURI:               trunc(r.ClientURI, 512),
		LogoURI:                 trunc(r.LogoURI, 512),
	}
	if out.Scope == "" {
		out.Scope = ScopeMCPRead
	}
	return out, nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
