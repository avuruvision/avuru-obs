//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestAuthTokenLifecycle(t *testing.T) {
	store := startClickHouse(t) // migrates otel.auth_token via the real migrator
	ctx := context.Background()

	// Second granularity: these inserts bind the way the rest of this package
	// does, which does not carry sub-second precision, so the store truncates
	// on the way in rather than pretending otherwise. Pinned below.
	created := time.Now().UTC().Truncate(time.Second)
	alice := storage.AuthToken{
		TokenHash: "hash-alice-1", UserID: "alice",
		Name: "ci-deploy-gate", Prefix: "avurut_al11", CreatedAt: created,
	}

	t.Run("create and get", func(t *testing.T) {
		if err := store.CreateAuthToken(ctx, alice); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := store.GetAuthTokenByHash(ctx, "hash-alice-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.UserID != "alice" || got.Name != "ci-deploy-gate" || got.Prefix != "avurut_al11" {
			t.Fatalf("unexpected: %+v", got)
		}
		// A token created with no expiry and never used must read back as the
		// Go zero time, not as the epoch the column actually stores. Getting
		// this wrong makes a never-expiring token look long expired.
		if !got.ExpiresAt.IsZero() {
			t.Errorf("ExpiresAt = %v, want zero (never expires)", got.ExpiresAt)
		}
		if !got.LastUsedAt.IsZero() {
			t.Errorf("LastUsedAt = %v, want zero (never used)", got.LastUsedAt)
		}
	})

	t.Run("list is scoped to the owner", func(t *testing.T) {
		bob := storage.AuthToken{
			TokenHash: "hash-bob-1", UserID: "bob",
			Name: "bob's laptop", Prefix: "avurut_bo11", CreatedAt: created,
		}
		second := storage.AuthToken{
			TokenHash: "hash-alice-2", UserID: "alice",
			Name: "nightly-report", Prefix: "avurut_al22",
			CreatedAt: created.Add(time.Second),
		}
		for _, tok := range []storage.AuthToken{bob, second} {
			if err := store.CreateAuthToken(ctx, tok); err != nil {
				t.Fatalf("create %s: %v", tok.TokenHash, err)
			}
		}
		list, err := store.ListAuthTokens(ctx, "alice")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("alice sees %d tokens, want 2: %+v", len(list), list)
		}
		// Newest first.
		if list[0].TokenHash != "hash-alice-2" {
			t.Errorf("list not newest-first: %+v", list)
		}
		for _, got := range list {
			if got.UserID != "alice" {
				t.Errorf("alice's list leaked %s's token: %+v", got.UserID, got)
			}
		}
	})

	t.Run("touch updates LastUsedAt without losing the rest of the row", func(t *testing.T) {
		used := created.Add(5 * time.Minute)
		if err := store.TouchAuthToken(ctx, "hash-alice-1", used); err != nil {
			t.Fatalf("touch: %v", err)
		}
		got, err := store.GetAuthTokenByHash(ctx, "hash-alice-1")
		if err != nil {
			t.Fatalf("get after touch: %v", err)
		}
		if !got.LastUsedAt.Equal(used) {
			t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
		}
		// The whole point of the read-then-write in TouchAuthToken: FINAL keeps
		// the newest ROW, not a column-wise merge, so a partial insert would
		// blank these and the token would keep working while losing its
		// identity and its expiry.
		if got.Name != "ci-deploy-gate" || got.Prefix != "avurut_al11" {
			t.Errorf("touch blanked identifying columns: %+v", got)
		}
		if !got.CreatedAt.Equal(created) {
			t.Errorf("touch blanked CreatedAt: got %v, want %v", got.CreatedAt, created)
		}
	})

	t.Run("an expired token still resolves and still lists", func(t *testing.T) {
		expired := storage.AuthToken{
			TokenHash: "hash-alice-old", UserID: "alice",
			Name: "retired", Prefix: "avurut_alold",
			ExpiresAt: created.Add(-24 * time.Hour), CreatedAt: created.Add(-48 * time.Hour),
		}
		if err := store.CreateAuthToken(ctx, expired); err != nil {
			t.Fatalf("create expired: %v", err)
		}
		// Storage does not decide expiry — the auth layer does. Filtering here
		// would leave the owner unable to see WHY their script stopped working.
		got, err := store.GetAuthTokenByHash(ctx, "hash-alice-old")
		if err != nil {
			t.Fatalf("expired token should still resolve at the storage layer: %v", err)
		}
		if !got.ExpiresAt.Equal(expired.ExpiresAt) {
			t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expired.ExpiresAt)
		}
		list, err := store.ListAuthTokens(ctx, "alice")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var found bool
		for _, tok := range list {
			if tok.TokenHash == "hash-alice-old" {
				found = true
			}
		}
		if !found {
			t.Errorf("expired token missing from its owner's list: %+v", list)
		}
	})

	t.Run("revoke tombstones and stops resolving", func(t *testing.T) {
		if err := store.RevokeAuthToken(ctx, "alice", "hash-alice-1"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := store.GetAuthTokenByHash(ctx, "hash-alice-1"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("revoked token still resolves: %v", err)
		}
		list, _ := store.ListAuthTokens(ctx, "alice")
		for _, tok := range list {
			if tok.TokenHash == "hash-alice-1" {
				t.Fatalf("revoked token still listed: %+v", list)
			}
		}
		// The owner's other tokens survive.
		if len(list) != 2 {
			t.Fatalf("revoke took out more than one token: %+v", list)
		}
	})

	t.Run("revoke is owner-scoped and idempotent-safe", func(t *testing.T) {
		// Alice cannot revoke Bob's token by guessing its hash.
		if err := store.RevokeAuthToken(ctx, "alice", "hash-bob-1"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("cross-owner revoke: want ErrNotFound, got %v", err)
		}
		if _, err := store.GetAuthTokenByHash(ctx, "hash-bob-1"); err != nil {
			t.Fatalf("bob's token was collateral damage: %v", err)
		}
		// Already-revoked / unknown → ErrNotFound.
		if err := store.RevokeAuthToken(ctx, "alice", "hash-alice-1"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("double revoke: want ErrNotFound, got %v", err)
		}
	})

	t.Run("touching a revoked token is ErrNotFound", func(t *testing.T) {
		if err := store.TouchAuthToken(ctx, "hash-alice-1", time.Now()); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("touch revoked: want ErrNotFound, got %v", err)
		}
	})

	t.Run("sub-second expiry truncates down, never up", func(t *testing.T) {
		// A token must not outlive the instant its owner was told it expires,
		// so a sub-second ExpiresAt has to land on or before the value given —
		// rounding up would hand out free milliseconds of validity.
		exact := created.Add(time.Hour)
		tok := storage.AuthToken{
			TokenHash: "hash-precision", UserID: "carol",
			Name: "precision", Prefix: "avurut_prec",
			ExpiresAt: exact.Add(900 * time.Millisecond), CreatedAt: created,
		}
		if err := store.CreateAuthToken(ctx, tok); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := store.GetAuthTokenByHash(ctx, "hash-precision")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ExpiresAt.After(tok.ExpiresAt) {
			t.Errorf("ExpiresAt %v is later than the requested %v", got.ExpiresAt, tok.ExpiresAt)
		}
		if !got.ExpiresAt.Equal(exact) {
			t.Errorf("ExpiresAt = %v, want it truncated to %v", got.ExpiresAt, exact)
		}
	})
}
