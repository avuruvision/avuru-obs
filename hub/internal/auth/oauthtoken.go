package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// OAuth token prefixes, one letter apart from APITokenPrefix and each other, so
// a secret found in a log or a repository says which credential leaked and
// therefore what to revoke — the same reasoning APITokenPrefix documents.
const (
	OAuthAccessPrefix  = "avuruo_"
	OAuthRefreshPrefix = "avurur_"
)

// NewOAuthToken returns (raw, hash) for one opaque token: 24 bytes from
// crypto/rand, base64url, hashed for storage exactly as an API token is. No
// per-token salt — these are high-entropy randoms, not passwords.
//
// Opaque rather than a JWT on purpose. Audience, scope and project live in the
// row this hash finds and are read on EVERY request, so revoking a grant or
// disabling a user takes effect on the next call. A JWT would freeze that
// authorization into a claim, which is what design/2026-08-13-api-tokens.md
// refuses — and it would need a signing key, a JWKS endpoint and key rotation
// to say something the database already knows.
func NewOAuthToken(prefix string) (raw, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = prefix + base64.RawURLEncoding.EncodeToString(b)
	// Same hash as every other bearer secret in the hub: one function, so a
	// change to it cannot apply to some credentials and not others.
	return raw, HashAPIToken(raw)
}

// OAuthBinding is what an access token authorizes, read fresh from its row.
// It is deliberately NOT part of Identity: Identity is the enterprise seam and
// is serialized by /auth/me, and an OAuth concept has no business on every
// client's view of who it is.
type OAuthBinding struct {
	Resource string
	Scope    string
	Project  string
	ClientID string
	GrantID  string
}

// IdentityFromOAuthToken resolves an opaque access token to its owner's
// identity and what that token may reach.
//
// The tail is IdentityFromAPIToken's, deliberately: token → UserID →
// GetAuthUser → identityFor, with nothing about a grant read off the token row.
// That is what makes "disable the user and every credential they hold stops
// working" true for OAuth as well.
//
// The BINDING is separate and additive: it can only narrow what the identity
// already allows, never widen it. An unknown, revoked or expired token, a
// revoked grant and a disabled owner are all storage.ErrNotFound and
// indistinguishable to the caller — the same stance sessions and API tokens
// take, so the endpoint is not an oracle for which of those is true.
func (s *Service) IdentityFromOAuthToken(ctx context.Context, raw string) (Identity, OAuthBinding, error) {
	st, err := s.st()
	if err != nil {
		return Identity{}, OAuthBinding{}, err
	}
	tok, err := st.GetOAuthTokenByHash(ctx, HashAPIToken(raw))
	if err != nil {
		return Identity{}, OAuthBinding{}, err
	}
	// Access tokens are short-lived by design; a refresh token presented here
	// is not an access token and must not be honoured as one.
	if tok.Kind != storage.OAuthTokenAccess {
		return Identity{}, OAuthBinding{}, storage.ErrNotFound
	}
	if !tok.ExpiresAt.IsZero() && tok.ExpiresAt.Before(time.Now()) {
		return Identity{}, OAuthBinding{}, storage.ErrNotFound
	}
	// The grant is the unit a person revokes in "connected applications", so it
	// is checked on every call rather than trusted at mint time.
	grant, err := st.GetOAuthGrant(ctx, tok.GrantID)
	if err != nil {
		return Identity{}, OAuthBinding{}, err
	}
	if grant.Revoked {
		return Identity{}, OAuthBinding{}, storage.ErrNotFound
	}
	u, err := st.GetAuthUser(ctx, tok.UserID)
	if err != nil {
		return Identity{}, OAuthBinding{}, err
	}
	if u.Disabled {
		return Identity{}, OAuthBinding{}, storage.ErrNotFound
	}
	id, err := s.identityFor(ctx, st, u)
	if err != nil {
		return Identity{}, OAuthBinding{}, err
	}
	return id, OAuthBinding{
		Resource: tok.Resource,
		Scope:    tok.Scope,
		Project:  tok.Project,
		ClientID: tok.ClientID,
		GrantID:  tok.GrantID,
	}, nil
}
