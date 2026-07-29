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
	"sync/atomic"
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
	// mapper turns an SSO user's IdP groups into grants at read time. Stored
	// as a pointer so it hot-reloads when OIDC config changes; nil until an
	// OIDC provider is wired up. See CompleteSSO / identityFor.
	mapper atomic.Pointer[func(groups []string) []Grant]
}

// SetGroupMapper installs (or replaces) the OIDC group→grants mapper. Grants
// for SSO users are derived from their stored groups on every identity read, so
// a mapping change takes effect on the next request without rewriting any rows.
func (s *Service) SetGroupMapper(m func(groups []string) []Grant) { s.mapper.Store(&m) }

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
	if s.limiter.blocked(key, ip) {
		return "", Identity{}, ErrTooManyAttempts
	}
	st, err := s.st()
	if err != nil {
		return "", Identity{}, err
	}
	u, err := st.GetAuthUserByEmail(ctx, email)
	if errors.Is(err, storage.ErrNotFound) {
		CheckDummy(password) // constant-shape timing
		s.limiter.fail(key, ip)
		return "", Identity{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Identity{}, fmt.Errorf("looking up user: %w", err)
	}
	// CheckPassword runs BEFORE the Disabled check so a disabled account
	// answers in the same ~bcrypt time as a wrong password (no status oracle).
	if !CheckPassword(u.PasswordHash, password) || u.Disabled {
		s.limiter.fail(key, ip)
		return "", Identity{}, ErrInvalidCredentials
	}
	id, err := s.identityFor(ctx, st, u)
	if err != nil {
		return "", Identity{}, err
	}
	token, err := s.mintSession(ctx, st, u.ID)
	if err != nil {
		return "", Identity{}, err
	}
	return token, id, nil
}

// mintSession creates a session row for userID and returns the raw cookie token.
func (s *Service) mintSession(ctx context.Context, st storage.Store, userID string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	sess := storage.AuthSession{
		TokenHash: hash,
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(s.ttl),
	}
	if err := st.CreateAuthSession(ctx, sess); err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return token, nil
}

// ExternalIdentity is the normalized result of an OIDC login.
type ExternalIdentity struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
}

// CompleteSSO upserts the SSO user (origin=oidc, groups refreshed), then mints
// the same session a local login would. Grants are NOT written here — identityFor
// derives mapped grants from the stored groups + current mapper, so manual grants
// stay intact.
func (s *Service) CompleteSSO(ctx context.Context, ext ExternalIdentity) (string, Identity, error) {
	st, err := s.st()
	if err != nil {
		return "", Identity{}, err
	}
	uid := "oidc|" + ext.Subject
	u := storage.AuthUser{ID: uid, Email: ext.Email, Name: ext.Name, Origin: "oidc", OidcGroups: ext.Groups}
	if existing, err := st.GetAuthUser(ctx, uid); err == nil {
		u.Disabled = existing.Disabled
	} else if !errors.Is(err, storage.ErrNotFound) {
		return "", Identity{}, fmt.Errorf("looking up sso user: %w", err)
	}
	if u.Disabled {
		return "", Identity{}, ErrInvalidCredentials
	}
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return "", Identity{}, fmt.Errorf("saving sso user: %w", err)
	}
	id, err := s.identityFor(ctx, st, u)
	if err != nil {
		return "", Identity{}, err
	}
	token, err := s.mintSession(ctx, st, uid)
	if err != nil {
		return "", Identity{}, err
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
// once at startup, after the store connects. created reports whether THIS
// call is the one that created the admin (false, nil means a user already
// existed) — callers that generate a random password need this to know
// whether to disclose it.
func (s *Service) Bootstrap(ctx context.Context, adminPassword string) (created bool, err error) {
	st, err := s.st()
	if err != nil {
		return false, err
	}
	n, err := st.CountAuthUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	hash, err := HashPassword(adminPassword)
	if err != nil {
		return false, err
	}
	// Grant first: if we crash between the writes, the next boot still sees
	// zero users and retries; an orphan grant row is harmless.
	if err := st.ReplaceAuthGrants(ctx, bootstrapAdminID, []storage.AuthGrant{
		{UserID: bootstrapAdminID, Scope: "*", Role: string(RoleAdmin)},
	}); err != nil {
		return false, fmt.Errorf("granting admin: %w", err)
	}
	u := storage.AuthUser{ID: bootstrapAdminID, Email: "admin", Name: "Administrator",
		PasswordHash: hash, Origin: "local"}
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return false, fmt.Errorf("creating admin: %w", err)
	}
	slog.Info("bootstrap: created admin user", "email", "admin")
	return true, nil
}

// demoViewerID is the FIXED id for the demo viewer (same rationale as
// bootstrapAdminID: a random id would let two replicas create divergent rows).
const demoViewerID = "demo-viewer"

// EnsureDemoUser idempotently creates/refreshes the read-only demo user
// (viewer @ "demo") from the configured credentials. Called at startup only
// when demo mode is enabled. Upsert-by-fixed-id keeps it safe under replicas and
// re-runnable on every boot (the chart owns the password).
func (s *Service) EnsureDemoUser(ctx context.Context, email, password string) error {
	st, err := s.st()
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	// Grant first (harmless orphan if we crash before the user write).
	if err := st.ReplaceAuthGrants(ctx, demoViewerID, []storage.AuthGrant{
		{UserID: demoViewerID, Scope: "demo", Role: string(RoleViewer)},
	}); err != nil {
		return fmt.Errorf("granting demo viewer: %w", err)
	}
	u := storage.AuthUser{ID: demoViewerID, Email: email, Name: "Demo (read-only)",
		PasswordHash: hash, Origin: "local"}
	if err := st.SaveAuthUser(ctx, u); err != nil {
		return fmt.Errorf("creating demo user: %w", err)
	}
	slog.Info("demo mode: ensured demo viewer", "email", email)
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
	// SSO users get their group-mapped grants derived at read time, so a
	// mapping change hot-reloads and manual grants (above) stay independent.
	if m := s.mapper.Load(); m != nil && len(u.OidcGroups) > 0 {
		id.Grants = append(id.Grants, (*m)(u.OidcGroups)...)
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
