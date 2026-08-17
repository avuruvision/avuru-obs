package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// APITokenPrefix is the human-visible prefix on every raw API token —
// deliberately one letter off IngestKeyPrefix so a secret found in a log or a
// repo says which kind of credential leaked, and therefore what to revoke.
const APITokenPrefix = "avurut_"

// NewAPIToken returns (raw, prefix, hash), mirroring NewIngestKey exactly: 24
// bytes from crypto/rand, base64url, no per-key salt (these are high-entropy
// randoms, not passwords). The raw token is shown to its owner exactly once
// at creation; only hash is persisted. prefix (the first 12 chars) is stored
// in clear for UI identification.
func NewAPIToken() (raw, prefix, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, raw[:12], HashAPIToken(raw)
}

// HashAPIToken is the storage/lookup hash (hex sha256). Lookups are by exact
// hash — no per-key salt, matching HashIngestKey's reasoning.
func HashAPIToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// IdentityFromAPIToken resolves a raw bearer token to its owner's identity,
// reading the owner and their grants fresh on every call: token → UserID →
// GetAuthUser → identityFor, the same tail IdentityFromToken runs for
// sessions. Nothing about a grant is ever read off the token row — there is
// no such column — which is what makes "disable the user or drop their role
// and every token they hold stops working" true rather than aspirational.
//
// An unknown token, a revoked token, an expired token, and a disabled owner
// all resolve to storage.ErrNotFound, indistinguishable to the caller — the
// same stance IdentityFromToken takes for sessions.
func (s *Service) IdentityFromAPIToken(ctx context.Context, raw string) (Identity, error) {
	st, err := s.st()
	if err != nil {
		return Identity{}, err
	}
	tok, err := st.GetAuthTokenByHash(ctx, HashAPIToken(raw))
	if err != nil {
		return Identity{}, err
	}
	// GetAuthTokenByHash deliberately returns an expired row without error
	// (storage.Store's doc comment) so the owner's token list can still show
	// why a token stopped working. Deciding "expired" is this layer's job. A
	// zero ExpiresAt means never expires — it must never be read as "expired
	// in 1970".
	if !tok.ExpiresAt.IsZero() && tok.ExpiresAt.Before(time.Now()) {
		return Identity{}, storage.ErrNotFound
	}
	u, err := st.GetAuthUser(ctx, tok.UserID)
	if err != nil {
		return Identity{}, err
	}
	if u.Disabled {
		return Identity{}, storage.ErrNotFound
	}
	return s.identityFor(ctx, st, u)
}
