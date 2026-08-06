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
	// ErrExternalPassword: the account's credential lives at the IdP.
	ErrExternalPassword = errors.New("password is managed by the identity provider")
	// ErrDemoUser: the shared demo account must not be re-keyed by a visitor.
	ErrDemoUser = errors.New("the demo account cannot change its password")
	// ErrPasswordUnusable: the NEW password was rejected by the hasher
	// (bcrypt refuses >72 bytes). A caller error, distinct from a wrong
	// current password — they must not share a message.
	ErrPasswordUnusable = errors.New("the new password cannot be used")
)

// bootstrapAdminID is the FIXED id used to create the bootstrap admin user.
//
// The bootstrap admin uses a FIXED id: two replicas racing past the
// "no operator-created user yet" check then insert the same
// ReplacingMergeTree key and collapse to one row — a random id would leave
// two divergent 'admin' users, one keeping the bootstrap password forever.
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
	// CheckPassword runs BEFORE the Disabled and Origin checks so a disabled
	// or IdP-governed account answers in the same ~bcrypt time as a wrong
	// password (no status oracle) — CheckPassword burns a dummy hash when the
	// stored one is empty, which is exactly the SSO case, so the cost is the
	// same on every branch.
	//
	// Origin is checked explicitly rather than left to "an SSO row has an
	// empty hash": that is an accident of provisioning, not an invariant.
	// Anything that ever writes a hash onto a non-local row — a future import
	// path, a hand-written row — would silently turn password login back on
	// for an account the IdP is supposed to own. Allow-listed on "local" so a
	// future origin defaults to refused.
	if !CheckPassword(u.PasswordHash, password) || u.Disabled || u.Origin != "local" {
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

// ChangePassword rotates userID's own password after verifying the current
// one. Success revokes EVERY existing session (an attacker holding a stolen
// cookie is evicted) and mints a fresh one so the legitimate caller stays
// signed in. Failed current-password checks feed the rate limiter — a stolen
// session must not become a password-guessing oracle.
//
// userID MUST come from the authenticated identity, never from client input.
// The not-found, origin and demo guards all answer before the bcrypt burn, so
// a caller free to name any user could enumerate which accounts exist and how
// each is governed — by status and by response time alike.
//
// Every rejection returns BEFORE the first write. After it there are two
// seams, both logged because neither reaches the caller as what it actually
// is: a failed revoke leaves the password rotated with stale sessions alive,
// and a failed mint leaves the caller signed out holding a password they
// believe never took. Save-then-revoke is still the right order — revoking
// first would log the caller out for a change that never landed.
func (s *Service) ChangePassword(ctx context.Context, userID, current, newPw, ip string) (string, error) {
	// Namespaced away from Login's buckets on BOTH axes. Behind an ingress
	// every client shares one ip, so a shared per-IP bucket would let 30
	// failed logins a minute disable self-service rotation deployment-wide —
	// unavailable exactly during the credential-stuffing incident that makes
	// rotating urgent. The prefix also removes a collision between this key
	// space and Login's, where an admin-created email could equal another
	// user's id. The path keeps its own 30/min ceiling, so the bcrypt cost cap
	// survives the split.
	key, ipKey := "pw|"+userID+"|"+ip, "pw|"+ip
	if s.limiter.blocked(key, ipKey) {
		return "", ErrTooManyAttempts
	}
	st, err := s.st()
	if err != nil {
		return "", err
	}
	u, err := st.GetAuthUser(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	// Allow-listed on "local" (not "!= oidc") so a future origin defaults to
	// refused. Login rejects non-local origins as well, so this is one of two
	// independent guards: this one keeps a hash off an IdP-governed row at
	// all, and Login refuses to honour one that somehow got there.
	if u.Origin != "local" {
		return "", ErrExternalPassword
	}
	// By id, not by origin — EnsureDemoUser creates the demo viewer as a
	// perfectly ordinary local user, so it clears the check above and only the
	// fixed id can single it out here. It also re-keys the account from the
	// configured credentials on every boot, so a "successful" change would
	// silently revert. The id is server-reserved, so this stays refused even
	// with demo mode since switched off — the conservative direction.
	if u.ID == DemoViewerID {
		return "", ErrDemoUser
	}
	// Not an authorization guard — a disabled user cannot hold a session to
	// reach this at all. It is the READ half of a read-modify-write:
	// SaveAuthUser rewrites the WHOLE row, so proceeding from a disabled copy
	// would write Disabled=false back and silently undo an admin's lockout.
	// Answered as ErrInvalidCredentials so account status stays indistinguishable.
	if u.Disabled {
		return "", ErrInvalidCredentials
	}
	if !CheckPassword(u.PasswordHash, current) {
		s.limiter.fail(key, ipKey)
		return "", ErrInvalidCredentials
	}
	// bcrypt hashes "" happily, and CheckPassword then accepts "" against that
	// hash — an empty password is no credential at all. This is an exported
	// credential API: it cannot rely on a handler to have filtered its input.
	if newPw == "" {
		return "", ErrPasswordUnusable
	}
	// Hash before saving anything: bcrypt refuses >72 bytes, and that is the
	// caller's error about the NEW password — it must never surface as
	// ErrInvalidCredentials, which reads as "your current password is wrong".
	hash, err := HashPassword(newPw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPasswordUnusable, err)
	}
	// Re-read: the row may have been disabled or deleted during the ~500ms of
	// bcrypt above, and SaveAuthUser writes the WHOLE row (Disabled and the
	// Deleted tombstone included, neither being in its column list), so a
	// stale copy would undo either. A PasswordHash that moved under us means
	// an admin reset the account mid-flight — the "current" password just
	// verified is no longer current.
	fresh, err := st.GetAuthUser(ctx, userID)
	if err != nil || fresh.Disabled || fresh.PasswordHash != u.PasswordHash {
		return "", ErrInvalidCredentials
	}
	fresh.PasswordHash = hash
	// Two bcrypt ops precede this point, so a client disconnect or proxy
	// timeout landing in the window below is ordinary tail behavior — and a
	// cancelled context alone would strand the rotation in exactly the
	// half-applied states the seams above describe. The write set must run to
	// completion even if the caller has hung up.
	ctx = context.WithoutCancel(ctx)
	if err := st.SaveAuthUser(ctx, fresh); err != nil {
		return "", fmt.Errorf("saving user: %w", err)
	}
	if err := st.RevokeAuthSessionsForUser(ctx, u.ID); err != nil {
		slog.Error("password rotated but sessions could not be revoked — existing cookies remain valid",
			"user", u.ID, "error", err)
		return "", fmt.Errorf("revoking sessions: %w", err)
	}
	token, err := s.mintSession(ctx, st, u.ID)
	if err != nil {
		slog.Error("password rotated and sessions revoked but no new session could be minted — the caller is signed out and must use the NEW password",
			"user", u.ID, "error", err)
		return "", err
	}
	return token, nil
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

// Bootstrap creates the global-admin `admin` user when no operator-created
// user exists. Called once at startup, after the store connects. created
// reports whether THIS call is the one that created the admin (false, nil
// means a user already existed) — callers that generate a random password
// need this to know whether to disclose it.
//
// The demo viewer is excluded from that check: the server creates it itself
// and refreshes it on every boot, so it is no evidence that an admin exists.
// It is also written by ensureDemoUser, a SIBLING GOROUTINE of the admin
// bootstrap (cmd/hub/main.go), so on a fresh demo-enabled install it wins the
// race often enough to be the norm rather than the exception. Counting it
// left such installs with no admin at all and no way back: the guard stayed
// satisfied on every subsequent boot, so the account could only be created by
// hand-writing the row. Ignoring it makes the outcome independent of who wins
// the race AND self-healing on the next restart for installs already in that
// state.
func (s *Service) Bootstrap(ctx context.Context, adminPassword string) (created bool, err error) {
	st, err := s.st()
	if err != nil {
		return false, err
	}
	// Listing rather than counting: the exclusion is by id, and the table is
	// tiny (the same assumption that lets email uniqueness live in the app
	// layer and every read use FINAL).
	users, err := st.ListAuthUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("listing users: %w", err)
	}
	for _, u := range users {
		if u.ID != DemoViewerID {
			return false, nil
		}
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

// DemoViewerID is the server-reserved id of the shared demo account. It is
// FIXED for the same reason as bootstrapAdminID (a random id would let two
// replicas create divergent rows), and exported because guards outside this
// package must recognize the demo row too. Recognize it BY THIS ID, never by
// a config value: EnsureDemoUser re-creates and re-keys the row from the
// chart's credentials on every boot, so it stays server-managed no matter what
// DemoEmail currently says or whether demo mode is switched on at all — while
// an email comparison would both miss the demo row after the address changes
// and wrongly capture an unrelated user who happens to share it.
const DemoViewerID = "demo-viewer"

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
	if err := st.ReplaceAuthGrants(ctx, DemoViewerID, []storage.AuthGrant{
		{UserID: DemoViewerID, Scope: "demo", Role: string(RoleViewer)},
	}); err != nil {
		return fmt.Errorf("granting demo viewer: %w", err)
	}
	u := storage.AuthUser{ID: DemoViewerID, Email: email, Name: "Demo (read-only)",
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
	id := Identity{UserID: u.ID, Email: u.Email, Name: u.Name, Origin: u.Origin}
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
