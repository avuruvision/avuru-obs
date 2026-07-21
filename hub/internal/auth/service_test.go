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
	if err := svc.Bootstrap(ctx, "root-pw"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(f.SavedUsers) != 1 || f.SavedUsers[0].Email != "admin" {
		t.Fatalf("saved: %+v", f.SavedUsers)
	}
	if g := f.Grants[f.SavedUsers[0].ID]; len(g) != 1 || g[0].Scope != "*" || g[0].Role != "admin" {
		t.Fatalf("admin grants: %+v", g)
	}
	// Second call is a no-op (users exist).
	if err := svc.Bootstrap(ctx, "other"); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	if len(f.SavedUsers) != 1 {
		t.Fatalf("re-bootstrap created a user: %+v", f.SavedUsers)
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
	if err := svc.Bootstrap(ctx, "pw"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Bootstrap: got %v, want ErrStoreUnavailable", err)
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

// TestRateLimitPerIPCap proves the second (per-IP) axis: an attacker who
// sprays a UNIQUE, never-seen email on every request never trips the
// per-account "email|ip" window (each key is fresh), yet must still be
// capped — otherwise they get one free bcrypt hash per request forever from
// a single IP.
func TestRateLimitPerIPCap(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	for i := 0; i < maxLoginAttemptsPerIP; i++ {
		email := fmt.Sprintf("spray%d@x.io", i)
		_, _, _ = svc.Login(ctx, email, "wrong", "shared-ip")
	}
	if _, _, err := svc.Login(ctx, "yet-another@x.io", "wrong", "shared-ip"); err != ErrTooManyAttempts {
		t.Fatalf("after %d unique-email failures from one ip: got %v, want ErrTooManyAttempts", maxLoginAttemptsPerIP, err)
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
