package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestNewAPIToken(t *testing.T) {
	raw, prefix, hash := NewAPIToken()
	if !strings.HasPrefix(raw, "avurut_") {
		t.Fatalf("raw missing prefix: %s", raw)
	}
	if prefix != raw[:12] {
		t.Fatalf("prefix = %s, want %s", prefix, raw[:12])
	}
	if hash != HashAPIToken(raw) {
		t.Fatal("hash mismatch")
	}
	if len(hash) != 64 {
		t.Fatalf("hash not hex sha256: %q (len %d)", hash, len(hash))
	}
	// Distinct each call.
	raw2, _, _ := NewAPIToken()
	if raw == raw2 {
		t.Fatal("tokens not random")
	}
}

// seedAPIToken creates userID (if not already present) with the given grants,
// stores an AuthToken row for a freshly minted raw token, and returns the raw
// token. expiresAt zero means "never expires", matching AuthToken's doc.
func seedAPIToken(t *testing.T, f *storagetest.Fake, userID string, grants []storage.AuthGrant, expiresAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	if _, ok := f.Users[userID]; !ok {
		if err := f.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: userID + "@x.io", Name: userID, Origin: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.ReplaceAuthGrants(ctx, userID, grants); err != nil {
		t.Fatal(err)
	}
	raw, prefix, hash := NewAPIToken()
	tok := storage.AuthToken{
		TokenHash: hash,
		UserID:    userID,
		Name:      "test token",
		Prefix:    prefix,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	if err := f.CreateAuthToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestAPITokenGrantsAreLiveNotSnapshotted is the security property the whole
// design rests on: resolution must read the owner's CURRENT grants on every
// call, never anything captured at mint time. If this silently snapshotted,
// revoking a role would not revoke the token.
func TestAPITokenGrantsAreLiveNotSnapshotted(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	// Zero ExpiresAt: never expires. Exercised explicitly here rather than
	// left implicit, since a zero time.Time must not be read as "expired in
	// 1970".
	raw := seedAPIToken(t, f, "u-1", []storage.AuthGrant{{UserID: "u-1", Scope: "demo", Role: "viewer"}}, time.Time{})

	id, err := svc.IdentityFromAPIToken(ctx, raw)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if len(id.Grants) != 1 || id.Grants[0].Scope != "demo" || id.Grants[0].Role != RoleViewer {
		t.Fatalf("first resolve grants = %+v", id.Grants)
	}

	// The owner's role changes AFTER the token was minted and used once. The
	// token row itself is untouched — only the owner's grants move.
	if err := f.ReplaceAuthGrants(ctx, "u-1", []storage.AuthGrant{{UserID: "u-1", Scope: "*", Role: "admin"}}); err != nil {
		t.Fatal(err)
	}

	id2, err := svc.IdentityFromAPIToken(ctx, raw)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if len(id2.Grants) != 1 || id2.Grants[0].Scope != "*" || id2.Grants[0].Role != RoleAdmin {
		t.Fatalf("second resolve did not reflect the owner's new grants: %+v", id2.Grants)
	}
}

func TestIdentityFromAPITokenUnknown(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	if _, err := svc.IdentityFromAPIToken(context.Background(), "avurut_does-not-exist"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestIdentityFromAPITokenRevoked(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	raw := seedAPIToken(t, f, "u-1", nil, time.Time{})
	if err := f.RevokeAuthToken(ctx, "u-1", HashAPIToken(raw)); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.IdentityFromAPIToken(ctx, raw); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("revoked token: got %v, want ErrNotFound", err)
	}
}

// TestIdentityFromAPITokenExpired proves the store/auth split described in
// storage.Store's doc comment: GetAuthTokenByHash returns an expired row
// without error (so the owner's token list can still show it), and it is
// this layer's job to turn that into ErrNotFound.
func TestIdentityFromAPITokenExpired(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	raw := seedAPIToken(t, f, "u-1", nil, time.Now().Add(-time.Hour))

	if _, err := f.GetAuthTokenByHash(ctx, HashAPIToken(raw)); err != nil {
		t.Fatalf("store must still return the expired row: %v", err)
	}

	if _, err := svc.IdentityFromAPIToken(ctx, raw); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expired token: got %v, want ErrNotFound", err)
	}
}

// TestIdentityFromAPITokenDisabledOwner applies the same rule
// IdentityFromToken already applies to sessions: a disabled owner's
// credentials stop resolving immediately.
func TestIdentityFromAPITokenDisabledOwner(t *testing.T) {
	f := &storagetest.Fake{}
	svc := testService(f)
	ctx := context.Background()

	raw := seedAPIToken(t, f, "u-1", []storage.AuthGrant{{UserID: "u-1", Scope: "demo", Role: "viewer"}}, time.Time{})

	u := f.Users["u-1"]
	u.Disabled = true
	if err := f.SaveAuthUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.IdentityFromAPIToken(ctx, raw); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("disabled owner: got %v, want ErrNotFound", err)
	}
}

// TestIdentityFromAPITokenStoreFailure is what lets the middleware answer 503
// instead of 401: a store failure must not look like ErrNotFound, or an
// automated caller would be told to re-authenticate against a hub that is
// merely ill.
func TestIdentityFromAPITokenStoreFailure(t *testing.T) {
	f := &storagetest.Fake{AuthTokensErr: errors.New("clickhouse unreachable")}
	svc := testService(f)

	_, err := svc.IdentityFromAPIToken(context.Background(), "avurut_whatever")
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("store failure must not present as ErrNotFound: %v", err)
	}
}
