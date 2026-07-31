package auth

import (
	"context"
	"errors"
	"fmt"
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
