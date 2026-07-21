package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

var (
	// ErrInvalidCredentials deliberately does not distinguish unknown user /
	// wrong password / disabled account.
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTooManyAttempts    = errors.New("too many login attempts")
	ErrStoreUnavailable   = errors.New("auth store unavailable")
)

// bootstrapAdminID is the FIXED id used to create the bootstrap admin user.
//
// The bootstrap admin uses a FIXED id: two replicas racing past the
// CountAuthUsers check then insert the same ReplacingMergeTree key and
// collapse to one row — a random id would leave two divergent 'admin'
// users, one keeping the bootstrap password forever.
const bootstrapAdminID = "bootstrap-admin"

// Service is the auth core: local login, sessions, bootstrap. Store is a
// provider (the hub's ClickHouse connection is established asynchronously).
type Service struct {
	store   func() storage.Store
	ttl     time.Duration
	limiter *rateLimiter
}

func NewService(store func() storage.Store, sessionTTL time.Duration) *Service {
	return &Service{store: store, ttl: sessionTTL, limiter: newRateLimiter()}
}

// SessionTTL exposes the configured session lifetime (cookie Max-Age mirrors it).
func (s *Service) SessionTTL() time.Duration { return s.ttl }

func (s *Service) st() (storage.Store, error) {
	if st := s.store(); st != nil {
		return st, nil
	}
	return nil, ErrStoreUnavailable
}

// Login authenticates email+password and mints a session token. ip feeds the
// rate limiter together with the email.
func (s *Service) Login(ctx context.Context, email, password, ip string) (string, Identity, error) {
	key := email + "|" + ip
	if s.limiter.blocked(key) {
		return "", Identity{}, ErrTooManyAttempts
	}
	st, err := s.st()
	if err != nil {
		return "", Identity{}, err
	}
	u, err := st.GetAuthUserByEmail(ctx, email)
	if errors.Is(err, storage.ErrNotFound) {
		CheckDummy(password) // constant-shape timing
		s.limiter.fail(key)
		return "", Identity{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Identity{}, fmt.Errorf("looking up user: %w", err)
	}
	// CheckPassword runs BEFORE the Disabled check so a disabled account
	// answers in the same ~bcrypt time as a wrong password (no status oracle).
	if !CheckPassword(u.PasswordHash, password) || u.Disabled {
		s.limiter.fail(key)
		return "", Identity{}, ErrInvalidCredentials
	}
	id, err := s.identityFor(ctx, st, u)
	if err != nil {
		return "", Identity{}, err
	}
	token, hash, err := newToken()
	if err != nil {
		return "", Identity{}, err
	}
	sess := storage.AuthSession{
		TokenHash: hash,
		UserID:    u.ID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(s.ttl),
	}
	if err := st.CreateAuthSession(ctx, sess); err != nil {
		return "", Identity{}, fmt.Errorf("creating session: %w", err)
	}
	return token, id, nil
}

// IdentityFromToken resolves a cookie token to a live identity. Revoked or
// expired sessions, and disabled users, fail with storage.ErrNotFound.
func (s *Service) IdentityFromToken(ctx context.Context, token string) (Identity, error) {
	st, err := s.st()
	if err != nil {
		return Identity{}, err
	}
	sess, err := st.GetAuthSession(ctx, hashToken(token))
	if err != nil {
		return Identity{}, err
	}
	u, err := st.GetAuthUser(ctx, sess.UserID)
	if err != nil {
		return Identity{}, err
	}
	if u.Disabled {
		return Identity{}, storage.ErrNotFound
	}
	return s.identityFor(ctx, st, u)
}

// Logout revokes the session; unknown tokens are a no-op (idempotent).
func (s *Service) Logout(ctx context.Context, token string) error {
	st, err := s.st()
	if err != nil {
		return err
	}
	err = st.RevokeAuthSession(ctx, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return err
}

// Bootstrap creates the global-admin `admin` user when no users exist. Called
// once at startup, after the store connects.
func (s *Service) Bootstrap(ctx context.Context, adminPassword string) error {
	st, err := s.st()
	if err != nil {
		return err
	}
	n, err := st.CountAuthUsers(ctx)
	if err != nil {
		return fmt.Errorf("counting users: %w", err)
	}
	if n > 0 {
		return nil
	}
	hash, err := HashPassword(adminPassword)
	if err != nil {
		return err
	}
	// Grant first: if we crash between the writes, the next boot still sees
	// zero users and retries; an orphan grant row is harmless.
	if err := st.ReplaceAuthGrants(ctx, bootstrapAdminID, []storage.AuthGrant{
		{UserID: bootstrapAdminID, Scope: "*", Role: string(RoleAdmin)},
	}); err != nil {
		return fmt.Errorf("granting admin: %w", err)
	}
	u := storage.AuthUser{ID: bootstrapAdminID, Email: "admin", Name: "Administrator",
		PasswordHash: hash, Origin: "local"}
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return fmt.Errorf("creating admin: %w", err)
	}
	slog.Info("bootstrap: created admin user", "email", "admin")
	return nil
}

func (s *Service) identityFor(ctx context.Context, st storage.Store, u storage.AuthUser) (Identity, error) {
	grants, err := st.ListAuthGrants(ctx, u.ID)
	if err != nil {
		return Identity{}, fmt.Errorf("listing grants: %w", err)
	}
	id := Identity{UserID: u.ID, Email: u.Email, Name: u.Name}
	for _, g := range grants {
		if r, ok := ParseRole(g.Role); ok {
			id.Grants = append(id.Grants, Grant{Scope: g.Scope, Role: r})
		}
	}
	return id, nil
}

// newToken returns (raw token for the cookie, hex sha256 for storage).
func newToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// NewID returns a 32-char random hex identifier.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}
