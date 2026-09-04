// Package oauth is the protocol half of the hub's OAuth 2.1 authorization
// server: metadata documents, client-registration validation, PKCE, and the
// vocabulary of errors. It owns no HTTP and no SQL, mirroring how
// hub/internal/mcp owns the MCP protocol and nothing else.
//
// It exists because a claude.ai connector — the client the MCP server's whole
// "off by default" warning was written for — cannot present a header-set API
// token. See design/2026-09-01-mcp-server.md, "Authorization, in two steps".
package oauth

import (
	"net/url"
	"strings"
	"time"
)

const (
	// ScopeMCPRead is the only scope this server issues. Read-only, and named
	// so a wider one later reads as a visible addition.
	ScopeMCPRead = "mcp:read"
	// ScopeOfflineAccess asks for a refresh token. Standard name, so a client
	// that already speaks OAuth needs no special case.
	ScopeOfflineAccess = "offline_access"

	// AccessTokenTTL is short because revocation is checked per request anyway;
	// the point of a short life is to bound a leaked token, not to force a
	// re-read of permissions.
	AccessTokenTTL = 15 * time.Minute
	// RefreshTokenTTL with rotation: each refresh mints a new one and revokes
	// the old, and presenting a rotated token revokes the whole family.
	RefreshTokenTTL = 30 * 24 * time.Hour
	// AuthCodeTTL is deliberately tiny. A code is exchanged immediately by a
	// client that just received it; anything longer is only useful to someone
	// who intercepted it.
	AuthCodeTTL = 60 * time.Second
)

// ResourceURI is the canonical identifier of the MCP endpoint: the RFC 8707
// `resource` value, the `resource` field of the protected-resource metadata,
// and the audience stamped on every access token.
//
// One function so those three can never disagree — an audience check that
// compares differently-normalised strings is a check that passes when it
// should not.
func ResourceURI(publicURL string) string {
	base := strings.TrimSuffix(strings.TrimSpace(publicURL), "/")
	if base == "" {
		return ""
	}
	return base + "/mcp"
}

// Issuer is the authorization server's identifier: the public origin, no path,
// no trailing slash.
func Issuer(publicURL string) string {
	return strings.TrimSuffix(strings.TrimSpace(publicURL), "/")
}

// ValidRedirectURI reports whether raw is a redirect URI this server will ever
// register or honour.
//
// HTTPS only, no fragment, no userinfo. Loopback HTTP is deliberately NOT
// allowed: it is what desktop clients want, claude.ai does not need it, and it
// widens the surface for no current caller. Adding it later is a decision with
// its own review.
func ValidRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.Host == "" {
		return false
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return false
	}
	if u.User != nil {
		return false
	}
	return true
}

// MatchRedirectURI compares a requested redirect against the registered set by
// EXACT string equality.
//
// Not prefix, not host-only, not "starts with one of these". A prefix match is
// how an open redirect gets built: a client registering
// https://app.example.com/cb would accept https://app.example.com/cb.evil.test
// under any looser rule, and the authorization code goes wherever this says.
func MatchRedirectURI(registered []string, requested string) bool {
	for _, r := range registered {
		if r == requested {
			return true
		}
	}
	return false
}

// NormalizeScope keeps only the scopes this server issues, preserving order and
// dropping duplicates. An unknown scope is not silently ignored by the caller —
// ScopeSupported reports it — but this is what gets stored once accepted.
func NormalizeScope(requested string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strings.Fields(requested) {
		if !ScopeSupported(s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// ScopeSupported reports whether this server issues the named scope.
func ScopeSupported(s string) bool {
	return s == ScopeMCPRead || s == ScopeOfflineAccess
}

// HasScope reports whether a space-delimited scope string contains want.
func HasScope(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}
