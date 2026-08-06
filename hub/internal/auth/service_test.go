package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func testService(f *storagetest.Fake) *Service {
	return NewService(func() storage.Store { return f }, 24*time.Hour)
}

// seedUser hashes with bcrypt.MinCost, not the real (cost-12) HashPassword,
// to keep this suite fast — bcrypt hashes embed their own cost, so
// CheckPassword behaves identically regardless. Real HashPassword is already
// exercised at cost 12 by TestPasswordHash (auth_test.go) and by
// Service.Bootstrap, which always calls it directly.
func seedUser(t *testing.T, f *storagetest.Fake, email, password string, grants []storage.AuthGrant) {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := storage.AuthUser{ID: "u-" + email, Email: email, Name: email, PasswordHash: string(h), Origin: "local"}
	if err := f.SaveAuthUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := f.ReplaceAuthGrants(context.Background(), u.ID, grants); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDemoUser(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	// The demo user exists, viewer-scoped to "demo" only.
	u, err := f.GetAuthUserByEmail(ctx, "demo@avuru.obs")
	if err != nil {
		t.Fatalf("demo user missing: %v", err)
	}
	grants, _ := f.ListAuthGrants(ctx, u.ID)
	if len(grants) != 1 || grants[0].Scope != "demo" || grants[0].Role != string(RoleViewer) {
		t.Fatalf("grants = %+v, want one viewer@demo", grants)
	}
	// It logs in with the configured password.
	if _, _, err := svc.Login(ctx, "demo@avuru.obs", "demo-pw", "1.2.3.4"); err != nil {
		t.Fatalf("demo login: %v", err)
	}
	// Idempotent: a second call doesn't duplicate or error.
	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginAndIdentity(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", []storage.AuthGrant{{UserID: "u-a@x.io", Scope: "demo", Role: "viewer"}})
	svc := testService(f)
	ctx := context.Background()

	token, id, err := svc.Login(ctx, "a@x.io", "pw", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if id.Email != "a@x.io" || len(id.Grants) != 1 || id.Grants[0].Scope != "demo" {
		t.Fatalf("identity: %+v", id)
	}

	got, err := svc.IdentityFromToken(ctx, token)
	if err != nil || got.UserID != id.UserID {
		t.Fatalf("token→identity: %+v err %v", got, err)
	}

	if _, _, err := svc.Login(ctx, "a@x.io", "wrong", "1.2.3.4"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: got %v", err)
	}
	if _, _, err := svc.Login(ctx, "ghost@x.io", "pw", "1.2.3.4"); err != ErrInvalidCredentials {
		t.Fatalf("unknown user: got %v", err)
	}

	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.IdentityFromToken(ctx, token); err == nil {
		t.Fatal("revoked token must not resolve")
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	u := f.UsersByEmail["a@x.io"]
	u.Disabled = true
	_ = f.SaveAuthUser(context.Background(), u)
	svc := testService(f)
	if _, _, err := svc.Login(context.Background(), "a@x.io", "pw", "ip"); err != ErrInvalidCredentials {
		t.Fatalf("disabled user login: got %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()
	for i := 0; i < maxLoginAttempts; i++ {
		_, _, _ = svc.Login(ctx, "a@x.io", "wrong", "ip")
	}
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip"); err != ErrTooManyAttempts {
		t.Fatalf("after %d failures: got %v, want ErrTooManyAttempts", maxLoginAttempts, err)
	}
}

func TestBootstrapCreatesAdminOnce(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()
	created, err := svc.Bootstrap(ctx, "root-pw")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created {
		t.Fatalf("first bootstrap: created=false, want true")
	}
	if len(f.SavedUsers) != 1 || f.SavedUsers[0].Email != "admin" {
		t.Fatalf("saved: %+v", f.SavedUsers)
	}
	if g := f.Grants[f.SavedUsers[0].ID]; len(g) != 1 || g[0].Scope != "*" || g[0].Role != "admin" {
		t.Fatalf("admin grants: %+v", g)
	}
	// Second call is a no-op (users exist).
	created, err = svc.Bootstrap(ctx, "other")
	if err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	if created {
		t.Fatalf("re-bootstrap: created=true, want false (users exist)")
	}
	if len(f.SavedUsers) != 1 {
		t.Fatalf("re-bootstrap created a user: %+v", f.SavedUsers)
	}
}

// The demo viewer must NOT satisfy the "already provisioned" guard: it is
// created by the server itself, not by an operator, so it is no evidence that
// an admin exists. bootstrapAdmin and ensureDemoUser run as concurrent
// goroutines (cmd/hub/main.go), so on a fresh install with auth.demo.enabled
// the demo write can land first — a plain user count then reads as "this
// install already has users", admin is never created, and every admin login
// fails with no way to recover short of hand-writing the row.
func TestBootstrapCreatesAdminWhenOnlyDemoUserExists(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	// The demo goroutine wins the race.
	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}

	created, err := svc.Bootstrap(ctx, "root-pw")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created {
		t.Fatal("bootstrap after the demo user landed: created=false, want true")
	}
	// The admin must be usable, not merely present.
	if _, _, err := svc.Login(ctx, "admin", "root-pw", "1.2.3.4"); err != nil {
		t.Fatalf("admin login after bootstrap: %v", err)
	}
	// The demo viewer is untouched by the admin bootstrap.
	if _, _, err := svc.Login(ctx, "demo@avuru.obs", "demo-pw", "1.2.3.4"); err != nil {
		t.Fatalf("demo login after bootstrap: %v", err)
	}
}

// The converse of the above: a REAL user still blocks the bootstrap. Skipping
// the demo viewer must not widen into "recreate admin whenever it is missing",
// which would hand a fresh admin password to an install whose operator
// deliberately runs without one (SSO-only, or admin disabled per the AEP).
func TestBootstrapSkipsWhenRealUserExists(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", []storage.AuthGrant{{UserID: "u-a@x.io", Scope: "*", Role: "admin"}})
	svc := testService(f)
	ctx := context.Background()

	if err := svc.EnsureDemoUser(ctx, "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}

	created, err := svc.Bootstrap(ctx, "root-pw")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if created {
		t.Fatal("bootstrap with a real user present: created=true, want false")
	}
}

func TestLogoutUnknownTokenIsNoop(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	if err := svc.Logout(context.Background(), "nope"); err != nil {
		t.Fatalf("logout unknown token: got %v, want nil", err)
	}
}

func TestStoreUnavailable(t *testing.T) {
	svc := NewService(func() storage.Store { return nil }, time.Hour)
	ctx := context.Background()

	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Login: got %v, want ErrStoreUnavailable", err)
	}
	if _, err := svc.IdentityFromToken(ctx, "tok"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("IdentityFromToken: got %v, want ErrStoreUnavailable", err)
	}
	if err := svc.Logout(ctx, "tok"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Logout: got %v, want ErrStoreUnavailable", err)
	}
	if created, err := svc.Bootstrap(ctx, "pw"); !errors.Is(err, ErrStoreUnavailable) || created {
		t.Fatalf("Bootstrap: got created=%v err=%v, want false, ErrStoreUnavailable", created, err)
	}
}

func TestDisabledUserTokenRejected(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	token, _, err := svc.Login(ctx, "a@x.io", "pw", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	u := f.UsersByEmail["a@x.io"]
	u.Disabled = true
	if err := f.SaveAuthUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.IdentityFromToken(ctx, token); err == nil {
		t.Fatal("token for a since-disabled user must not resolve")
	}
}

func TestRateLimitIsPerIP(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	for i := 0; i < maxLoginAttempts; i++ {
		_, _, _ = svc.Login(ctx, "a@x.io", "wrong", "A")
	}
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "A"); err != ErrTooManyAttempts {
		t.Fatalf("ip A: got %v, want ErrTooManyAttempts", err)
	}
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "B"); err != nil {
		t.Fatalf("ip B should be unaffected by A's failures: got %v", err)
	}
}

// TestRateLimiterPerIPCap proves the second (per-IP) axis DIRECTLY against
// the limiter: an attacker who sprays a UNIQUE, never-seen email on every
// request never trips the per-account "email|ip" window (each key is
// fresh), yet must still be capped — otherwise they get one free bcrypt
// hash per request forever from a single IP. It also proves the per-IP
// block self-heals once loginWindow elapses, via the injectable clock.
//
// Going straight to the limiter (no Login, no bcrypt) keeps this suite fast.
// Scope of what's actually pinned: the per-ACCOUNT axis's wiring through
// Login IS covered elsewhere — TestRateLimitIsPerIP at the service level,
// TestLoginRateLimit429 at the HTTP level. Login's wiring of ip into THIS
// (per-IP) axis is NOT separately pinned by any test that goes through
// Login — on record as a real gap, not asserted away by this test's name.
func TestRateLimiterPerIPCap(t *testing.T) {
	l := newRateLimiter()
	fakeNow := time.Now()
	l.now = func() time.Time { return fakeNow }

	for i := 0; i < maxLoginAttemptsPerIP; i++ {
		email := fmt.Sprintf("spray%d@x.io", i)
		l.fail(email+"|shared-ip", "shared-ip")
	}
	if !l.blocked("fresh@x.io|shared-ip", "shared-ip") {
		t.Fatalf("after %d unique-email failures from one ip: not blocked, want blocked", maxLoginAttemptsPerIP)
	}

	// The block self-heals once loginWindow elapses (fixed-window cap).
	fakeNow = fakeNow.Add(loginWindow + time.Second)
	if l.blocked("fresh@x.io|shared-ip", "shared-ip") {
		t.Fatal("per-ip block should clear after loginWindow elapses")
	}
}

func TestIdentityDropsInvalidRole(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", []storage.AuthGrant{{UserID: "u-a@x.io", Scope: "demo", Role: "superuser"}})
	svc := testService(f)

	_, id, err := svc.Login(context.Background(), "a@x.io", "pw", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(id.Grants) != 0 {
		t.Fatalf("invalid role grant must be dropped: %+v", id.Grants)
	}
}

func TestCompleteSSOUpsertsUserAndMapsGrants(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	svc.SetGroupMapper(func(groups []string) []Grant {
		for _, g := range groups {
			if g == "obs-admins" {
				return []Grant{{Scope: "*", Role: RoleAdmin}}
			}
		}
		return nil
	})
	ctx := context.Background()

	ext := ExternalIdentity{Subject: "kc|1", Email: "sso@x.io", Name: "SSO User", Groups: []string{"obs-admins"}}
	token, id, err := svc.CompleteSSO(ctx, ext)
	if err != nil {
		t.Fatalf("CompleteSSO: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty session token")
	}
	if !id.IsAdmin() {
		t.Fatalf("mapped grants missing admin: %+v", id.Grants)
	}

	// The minted session is the same kind a local login mints: the raw token
	// resolves back to the mapped identity.
	got, err := svc.IdentityFromToken(ctx, token)
	if err != nil {
		t.Fatalf("token→identity: %v", err)
	}
	if !got.IsAdmin() {
		t.Fatalf("resolved identity lost mapped admin grant: %+v", got.Grants)
	}

	// Idempotent upsert: a second SSO login reuses the same user row (fixed
	// oidc| id), so exactly one user carries this email.
	if _, _, err := svc.CompleteSSO(ctx, ext); err != nil {
		t.Fatalf("second CompleteSSO: %v", err)
	}
	users, err := f.ListAuthUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, u := range users {
		if u.Email == ext.Email {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one user with email %q, got %d", ext.Email, count)
	}
}

// TestCompleteSSOKeepsManualGrantsIndependent proves mapped grants are derived
// at read time (identityFor) rather than persisted: a manual grant added out of
// band survives an SSO re-login, and the mapped grant is NOT written to storage.
func TestCompleteSSOKeepsManualGrantsIndependent(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	svc.SetGroupMapper(func(groups []string) []Grant {
		for _, g := range groups {
			if g == "obs-admins" {
				return []Grant{{Scope: "*", Role: RoleAdmin}}
			}
		}
		return nil
	})
	ctx := context.Background()
	ext := ExternalIdentity{Subject: "kc|9", Email: "sso@x.io", Name: "SSO", Groups: []string{"obs-admins"}}

	if _, _, err := svc.CompleteSSO(ctx, ext); err != nil {
		t.Fatalf("CompleteSSO: %v", err)
	}
	uid := "oidc|" + ext.Subject
	// A manual, admin-assigned grant on a specific project.
	if err := f.ReplaceAuthGrants(ctx, uid, []storage.AuthGrant{{UserID: uid, Scope: "demo", Role: "editor"}}); err != nil {
		t.Fatal(err)
	}

	_, id, err := svc.CompleteSSO(ctx, ext)
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	// Both the manual editor-on-demo and the mapped admin-on-* are present.
	if !id.IsAdmin() {
		t.Fatalf("mapped admin missing after re-login: %+v", id.Grants)
	}
	if r, ok := id.RoleFor("demo"); !ok || r != RoleAdmin {
		t.Fatalf("manual demo grant clobbered: role=%v ok=%v grants=%+v", r, ok, id.Grants)
	}
	// The mapped grant is not persisted — storage still holds only the manual one.
	stored, err := f.ListAuthGrants(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Scope != "demo" {
		t.Fatalf("mapped grant leaked into storage: %+v", stored)
	}
}

// ChangePassword: the current password gates the rotation; success revokes
// every prior session and mints a fresh one. Deliberately end-to-end through
// Bootstrap + Login (real cost-12 hashes, unlike seedUser) — this is the one
// place that must prove the OLD password stops working and the NEW one starts.
func TestChangePasswordRotatesSessions(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()
	if _, err := svc.Bootstrap(ctx, "old-pw"); err != nil {
		t.Fatal(err)
	}
	oldToken, id, err := svc.Login(ctx, "admin", "old-pw", "ip1")
	if err != nil {
		t.Fatal(err)
	}

	newToken, err := svc.ChangePassword(ctx, id.UserID, "old-pw", "new-pw", "ip1")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := svc.IdentityFromToken(ctx, oldToken); err == nil {
		t.Fatal("the pre-rotation session survived")
	}
	if _, err := svc.IdentityFromToken(ctx, newToken); err != nil {
		t.Fatalf("the fresh session does not work: %v", err)
	}
	if _, _, err := svc.Login(ctx, "admin", "new-pw", "ip1"); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if _, _, err := svc.Login(ctx, "admin", "old-pw", "ip2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login with the old password: %v, want ErrInvalidCredentials", err)
	}
}

func TestChangePasswordGuards(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "old-pw", nil)
	svc := testService(f)
	ctx := context.Background()
	const uid = "u-a@x.io"

	// Wrong current password fails and counts toward the rate limiter — a
	// stolen session must not become a password-guessing oracle. The key space
	// is namespaced ("pw|"), deliberately separate from Login's, so the two
	// paths cannot starve each other; the 5-per-window ceiling is shared.
	if _, err := svc.ChangePassword(ctx, uid, "WRONG", "x", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current: %v, want ErrInvalidCredentials", err)
	}
	for i := 1; i < maxLoginAttempts; i++ {
		_, _ = svc.ChangePassword(ctx, uid, "WRONG", "x", "ip1")
	}
	if _, err := svc.ChangePassword(ctx, uid, "old-pw", "x-good", "ip1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("after %d failures: %v, want ErrTooManyAttempts", maxLoginAttempts, err)
	}
	// The lockout rejected that call BEFORE any write: the password is
	// untouched (checked from a clean ip, so the lockout itself can't answer).
	if _, _, err := svc.Login(ctx, "a@x.io", "old-pw", "ip-clean"); err != nil {
		t.Fatalf("a rate-limited change must not have applied: %v", err)
	}

	// SSO users have no local password. Their credential lives at the IdP, and
	// Login resolves by email without filtering Origin — writing a local hash
	// here would mint a working IdP-bypassing credential.
	if err := f.SaveAuthUser(ctx, storage.AuthUser{ID: "oidc|s", Email: "s@x.io", Origin: "oidc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(ctx, "oidc|s", "a", "b", "ip9"); !errors.Is(err, ErrExternalPassword) {
		t.Fatalf("sso user: %v, want ErrExternalPassword", err)
	}

	// The shared demo account must not be re-keyed by a visitor — refused even
	// with the CORRECT current password, so this is a guard, not a bad login.
	if err := svc.EnsureDemoUser(ctx, "demo@x.io", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(ctx, DemoViewerID, "demo-pw", "b", "ip9"); !errors.Is(err, ErrDemoUser) {
		t.Fatalf("demo user: %v, want ErrDemoUser", err)
	}
}

// A NEW password the hasher refuses (bcrypt caps at 72 bytes) is the CALLER's
// error, not a failed current-password check: the two must not share a
// sentinel, or the handler would tell a user with a 73-byte new password that
// their current one is wrong. Nothing may be written on that path either.
func TestChangePasswordRejectsUnusableNewPassword(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "old-pw", nil)
	svc := testService(f)
	ctx := context.Background()

	token, id, err := svc.Login(ctx, "a@x.io", "old-pw", "ip1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.ChangePassword(ctx, id.UserID, "old-pw", strings.Repeat("x", 73), "ip1")
	if !errors.Is(err, ErrPasswordUnusable) {
		t.Fatalf("73-byte new password: %v, want ErrPasswordUnusable", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("an unusable NEW password must not report the CURRENT one as wrong: %v", err)
	}
	// The empty password is the more dangerous end of the same check: bcrypt
	// hashes it happily and CheckPassword then accepts "" against that hash.
	if _, err := svc.ChangePassword(ctx, id.UserID, "old-pw", "", "ip1"); !errors.Is(err, ErrPasswordUnusable) {
		t.Fatalf("empty new password: %v, want ErrPasswordUnusable", err)
	}
	// The rejection happened before any write: the old password still logs in
	// and the existing session was not revoked.
	if _, _, err := svc.Login(ctx, "a@x.io", "old-pw", "ip2"); err != nil {
		t.Fatalf("old password after a rejected change: %v, want it to still work", err)
	}
	if _, err := svc.IdentityFromToken(ctx, token); err != nil {
		t.Fatalf("session revoked by a rejected change: %v", err)
	}
}

// readTrackingStore counts GetAuthUser calls and can flip Disabled on a
// chosen read, standing in for an admin disabling a compromised account
// during the ~500ms of bcrypt inside ChangePassword.
type readTrackingStore struct {
	*storagetest.Fake
	reads         int
	disableOnRead int // 0 = never
}

func (d *readTrackingStore) GetAuthUser(ctx context.Context, id string) (storage.AuthUser, error) {
	d.reads++
	if d.reads == d.disableOnRead {
		u := d.Users[id]
		u.Disabled = true
		if err := d.SaveAuthUser(ctx, u); err != nil {
			return storage.AuthUser{}, err
		}
	}
	return d.Fake.GetAuthUser(ctx, id)
}

// A disabled user must not rotate their password — not because they could
// reach this method (they cannot hold a session), but because SaveAuthUser
// rewrites the WHOLE row: proceeding from a disabled copy would write
// Disabled=false back and undo the admin's lockout.
func TestChangePasswordRefusesDisabledUser(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	d := &readTrackingStore{Fake: f}
	svc := NewService(func() storage.Store { return d }, 24*time.Hour)
	ctx := context.Background()

	u := f.UsersByEmail["a@x.io"]
	u.Disabled = true
	if err := f.SaveAuthUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	writes := len(f.SavedUsers)

	if _, err := svc.ChangePassword(ctx, u.ID, "pw", "new-pw", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user: %v, want ErrInvalidCredentials", err)
	}
	if len(f.SavedUsers) != writes {
		t.Fatalf("a disabled user's change wrote to the store: %+v", f.SavedUsers[writes:])
	}
	if got, _ := f.GetAuthUser(ctx, u.ID); !got.Disabled {
		t.Fatal("the lockout was undone")
	}
	// Refused on the FIRST read: a second one means the check slid below the
	// bcrypt work, where the pre-save re-read would mask it. The re-read is
	// the backstop for a row that changes mid-flight, not the primary guard.
	if d.reads != 1 {
		t.Fatalf("GetAuthUser calls = %d, want 1 (refused before any bcrypt)", d.reads)
	}
}

// The other half of the read-modify-write: the row may change AFTER the read
// that cleared it. Without the pre-save re-read the in-flight save writes the
// stale Disabled=false row back and undoes the lockout — and since
// SaveAuthUser's column list omits Deleted and UpdatedAt too, that same stale
// write resurrects a user deleted in the window, with a fresh password and a
// fresh session.
func TestChangePasswordRefusesRowDisabledMidFlight(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	d := &readTrackingStore{Fake: f, disableOnRead: 2}
	svc := NewService(func() storage.Store { return d }, 24*time.Hour)
	ctx := context.Background()

	const uid = "u-a@x.io"
	before := f.Users[uid].PasswordHash

	if _, err := svc.ChangePassword(ctx, uid, "pw", "new-pw", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled mid-flight: %v, want ErrInvalidCredentials", err)
	}
	if got := f.Users[uid]; got.PasswordHash != before {
		t.Fatal("the rotation landed on a row that was disabled under it")
	}
	if !f.Users[uid].Disabled {
		t.Fatal("the lockout was undone by the in-flight save")
	}
}

// hangupStore emulates a client disconnect landing in ChangePassword's write
// window: it cancels the request context as the pre-save re-read returns, then
// refuses writes on a dead context the way a real driver does (the fake
// ignores ctx entirely). A disconnect EARLIER is harmless — it aborts at the
// re-read with nothing written — so this is the one instant that matters.
type hangupStore struct {
	*storagetest.Fake
	cancel context.CancelFunc
	reads  int
}

func (h *hangupStore) GetAuthUser(ctx context.Context, id string) (storage.AuthUser, error) {
	h.reads++
	u, err := h.Fake.GetAuthUser(ctx, id)
	if h.reads == 2 {
		h.cancel() // the caller hangs up just as the writes are about to start
	}
	return u, err
}

func (h *hangupStore) SaveAuthUser(ctx context.Context, u storage.AuthUser) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Fake.SaveAuthUser(ctx, u)
}

func (h *hangupStore) RevokeAuthSessionsForUser(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Fake.RevokeAuthSessionsForUser(ctx, userID)
}

func (h *hangupStore) CreateAuthSession(ctx context.Context, s storage.AuthSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Fake.CreateAuthSession(ctx, s)
}

// Two bcrypt ops precede the writes, so a disconnect or proxy timeout in that
// window is ordinary tail behavior. The save/revoke/mint set must run to
// completion regardless, or the caller is left half-rotated.
func TestChangePasswordCompletesWhenCallerHangsUp(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewService(func() storage.Store { return &hangupStore{Fake: f, cancel: cancel} }, 24*time.Hour)

	token, err := svc.ChangePassword(ctx, "u-a@x.io", "pw", "new-pw", "ip1")
	if err != nil {
		t.Fatalf("caller hung up mid-rotation: %v, want the write set to finish anyway", err)
	}
	if _, err := svc.IdentityFromToken(context.Background(), token); err != nil {
		t.Fatalf("the fresh session was not minted: %v", err)
	}
	if _, _, err := svc.Login(context.Background(), "a@x.io", "new-pw", "ip2"); err != nil {
		t.Fatalf("the new password did not land: %v", err)
	}
}

// The limiter key is the userID, not the email Login uses, and lives in its
// own "pw|" namespace: guesses made here must neither exhaust nor be
// exhausted by the victim's login budget.
func TestChangePasswordDoesNotExhaustLoginBudget(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()
	const uid = "u-a@x.io"

	for i := 0; i < maxLoginAttempts; i++ {
		_, _ = svc.ChangePassword(ctx, uid, "WRONG", "x", "ip1")
	}
	// Same email, same ip, same window: the login budget is untouched.
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip1"); err != nil {
		t.Fatalf("login after %d failed password changes: %v, want it unaffected", maxLoginAttempts, err)
	}
	// ...yet this path's own account axis did lock.
	if _, err := svc.ChangePassword(ctx, uid, "pw", "new-pw", "ip1"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("change after %d failures: %v, want ErrTooManyAttempts", maxLoginAttempts, err)
	}
}

// Both limiter axes are namespaced per path. The per-IP one matters most:
// behind an ingress every client shares one ip, so a shared bucket would let
// a login flood disable password rotation for the whole deployment — exactly
// when rotating matters.
func TestChangePasswordLimiterIsNamespaced(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	seedUser(t, f, "b@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	// One user's failures do not lock another sharing the ip. Asserted with a
	// wrong password (ErrInvalidCredentials, not ErrTooManyAttempts) so the
	// limiter verdict is what's read, without paying for a cost-12 hash.
	for i := 0; i < maxLoginAttempts; i++ {
		_, _ = svc.ChangePassword(ctx, "u-a@x.io", "WRONG", "x", "shared-ip")
	}
	if _, err := svc.ChangePassword(ctx, "u-b@x.io", "WRONG", "x", "shared-ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second user on the same ip: %v, want ErrInvalidCredentials (not locked out)", err)
	}

	// A login flood saturating the per-IP axis (as TestRateLimiterPerIPCap
	// drives it) must leave rotation available. Straight to the limiter, no
	// bcrypt — the same idiom that test uses.
	for i := 0; i < maxLoginAttemptsPerIP; i++ {
		svc.limiter.fail(fmt.Sprintf("spray%d@x.io|flooded-ip", i), "flooded-ip")
	}
	if _, err := svc.ChangePassword(ctx, "u-b@x.io", "pw", "new-pw", "flooded-ip"); err != nil {
		t.Fatalf("rotation during a login flood on the same ip: %v, want it to still work", err)
	}
}

// Origin rides the identity so /auth/me can tell the SPA whether a password
// form even applies.
func TestIdentityCarriesOrigin(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	_, id, err := svc.Login(ctx, "a@x.io", "pw", "ip1")
	if err != nil {
		t.Fatal(err)
	}
	if id.Origin != "local" {
		t.Fatalf("Origin = %q, want local", id.Origin)
	}

	_, ssoID, err := svc.CompleteSSO(ctx, ExternalIdentity{Subject: "kc|7", Email: "sso@x.io"})
	if err != nil {
		t.Fatal(err)
	}
	if ssoID.Origin != "oidc" {
		t.Fatalf("sso Origin = %q, want oidc", ssoID.Origin)
	}
}

func TestRateLimitWindowExpiry(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	fakeNow := time.Now()
	svc.limiter.now = func() time.Time { return fakeNow }

	for i := 0; i < maxLoginAttempts; i++ {
		_, _, _ = svc.Login(ctx, "a@x.io", "wrong", "ip")
	}
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip"); err != ErrTooManyAttempts {
		t.Fatalf("before window expiry: got %v, want ErrTooManyAttempts", err)
	}

	fakeNow = fakeNow.Add(loginWindow + time.Second)
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip"); err != nil {
		t.Fatalf("after window expiry: got %v, want nil", err)
	}
}

// An SSO login whose IdP email matches an existing local account writes a
// SECOND auth_user row sharing that address (CompleteSSO upserts by
// "oidc|<sub>" and never consults the email). The local user must keep their
// password login: before GetAuthUserByEmail pinned an order, the lookup
// returned an arbitrary one of the two rows and the local account
// intermittently stopped authenticating.
func TestLoginSurvivesSSOEmailCollision(t *testing.T) {
	f := &storagetest.Fake{}
	seedUser(t, f, "a@x.io", "pw", nil)
	svc := testService(f)
	ctx := context.Background()

	// The SSO row sorts BEFORE the local one on id ("oidc|..." < "u-a@x.io"),
	// so a tiebreak on id alone would pick the wrong row — local-first is what
	// this asserts, not merely determinism.
	if _, _, err := svc.CompleteSSO(ctx, ExternalIdentity{
		Subject: "sub-1", Email: "a@x.io", Name: "Alice via IdP",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetAuthUser(ctx, "oidc|sub-1"); err != nil {
		t.Fatalf("sso row not created, collision not reproduced: %v", err)
	}

	u, err := f.GetAuthUserByEmail(ctx, "a@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "u-a@x.io" {
		t.Fatalf("lookup resolved to %q, want the local row u-a@x.io", u.ID)
	}
	// The whole point: the local user can still sign in with their password.
	if _, _, err := svc.Login(ctx, "a@x.io", "pw", "ip"); err != nil {
		t.Fatalf("local login after collision: %v", err)
	}
}

// Password login is allow-listed on origin=local. An SSO row today has an
// empty hash and would fail CheckPassword anyway — this asserts the guard does
// not DEPEND on that, since a hash reaching a non-local row (import path,
// hand-written row) would otherwise silently re-enable password login for an
// account the IdP owns.
func TestLoginRefusesNonLocalOriginEvenWithAWorkingHash(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	h, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "oidc|sub-2", Email: "sso@x.io", Name: "SSO",
		PasswordHash: string(h), Origin: "oidc",
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.Login(ctx, "sso@x.io", "pw", "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v, want ErrInvalidCredentials", err)
	}
	// An unknown origin must fail closed too (allow-list, not a deny-list).
	if err := f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "x-1", Email: "future@x.io", Name: "Future",
		PasswordHash: string(h), Origin: "ldap",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login(ctx, "future@x.io", "pw", "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown origin: got %v, want ErrInvalidCredentials", err)
	}
}

// brokenWriteStore fails exactly one of the two post-save writes, leaving the
// rotation half-applied the way a ClickHouse blip would.
type brokenWriteStore struct {
	*storagetest.Fake
	failRevoke bool
	failMint   bool
}

func (b *brokenWriteStore) RevokeAuthSessionsForUser(ctx context.Context, userID string) error {
	if b.failRevoke {
		return errors.New("clickhouse unavailable")
	}
	return b.Fake.RevokeAuthSessionsForUser(ctx, userID)
}

func (b *brokenWriteStore) CreateAuthSession(ctx context.Context, s storage.AuthSession) error {
	if b.failMint {
		return errors.New("clickhouse unavailable")
	}
	return b.Fake.CreateAuthSession(ctx, s)
}

// Both post-save failures must be reported as what they are. The password has
// already rotated at this point, so a generic error — which the handler renders
// as "internal error" — would tell the user nothing changed and send them back
// to the old password they can no longer use.
func TestChangePasswordReportsHalfAppliedRotations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(f *storagetest.Fake) storage.Store
		want  error
	}{
		{
			name:  "revoke fails: rotated, stale cookies still live",
			store: func(f *storagetest.Fake) storage.Store { return &brokenWriteStore{Fake: f, failRevoke: true} },
			want:  ErrRotatedSessionsLive,
		},
		{
			name:  "mint fails: rotated and swept, caller signed out",
			store: func(f *storagetest.Fake) storage.Store { return &brokenWriteStore{Fake: f, failMint: true} },
			want:  ErrRotatedButSignedOut,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &storagetest.Fake{}
			seedUser(t, f, "a@x.io", "pw", nil)
			st := tc.store(f)
			svc := NewService(func() storage.Store { return st }, 24*time.Hour)
			ctx := context.Background()

			token, err := svc.ChangePassword(ctx, "u-a@x.io", "pw", "new-pw", "ip1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if token != "" {
				t.Fatalf("token = %q, want empty on a failed rotation", token)
			}
			// The claim the sentinel makes must be TRUE: the new password works
			// and the old one does not. Asserted through Login (against the plain
			// fake, so the broken write path is out of the way) rather than by
			// peeking at the hash — that is what the user will experience.
			plain := testService(f)
			if _, _, err := plain.Login(ctx, "a@x.io", "new-pw", "ip2"); err != nil {
				t.Fatalf("the new password is not in effect: %v", err)
			}
			if _, _, err := plain.Login(ctx, "a@x.io", "pw", "ip3"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("the old password still works: %v", err)
			}
		})
	}
}
