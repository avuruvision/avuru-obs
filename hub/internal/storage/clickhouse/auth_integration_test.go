//go:build integration

package clickhouse

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestAuthRoundtrip exercises the auth storage seam end to end against a real
// ClickHouse: user upsert under ReplacingMergeTree FINAL semantics, grant
// replace-with-tombstone, and session create/get/revoke.
func TestAuthRoundtrip(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	t.Run("UserUpsertReadsNewest", func(t *testing.T) {
		u := storage.AuthUser{
			ID:           "u1",
			Email:        "alice@example.com",
			Name:         "Alice",
			PasswordHash: "hash1",
			Origin:       "local",
		}
		if err := store.SaveAuthUser(ctx, u); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}

		// Resave with a changed Name: ReplacingMergeTree keeps the newest row
		// by UpdatedAt, so a FINAL read must return the update, not the
		// original.
		u.Name = "Alice Updated"
		if err := store.SaveAuthUser(ctx, u); err != nil {
			t.Fatalf("SaveAuthUser (resave): %v", err)
		}

		got, err := store.GetAuthUser(ctx, "u1")
		if err != nil {
			t.Fatalf("GetAuthUser: %v", err)
		}
		if got.Name != "Alice Updated" {
			t.Errorf("Name = %q, want %q (FINAL should return the newest row)", got.Name, "Alice Updated")
		}
		if got.Email != "alice@example.com" || got.PasswordHash != "hash1" || got.Origin != "local" || got.Disabled {
			t.Errorf("user round-tripped wrong: %+v", got)
		}

		byEmail, err := store.GetAuthUserByEmail(ctx, "alice@example.com")
		if err != nil {
			t.Fatalf("GetAuthUserByEmail: %v", err)
		}
		if byEmail.ID != "u1" || byEmail.Name != "Alice Updated" {
			t.Errorf("GetAuthUserByEmail wrong: %+v", byEmail)
		}

		// ListAuthUsers doubles as the FINAL-collapse assertion: u1 was
		// written twice above and must come back as exactly one row.
		all, err := store.ListAuthUsers(ctx)
		if err != nil {
			t.Fatalf("ListAuthUsers: %v", err)
		}
		if len(all) != 1 || all[0].ID != "u1" {
			t.Errorf("ListAuthUsers wrong: %+v", all)
		}
	})

	t.Run("MissingUserIsErrNotFound", func(t *testing.T) {
		if _, err := store.GetAuthUser(ctx, "does-not-exist"); err != storage.ErrNotFound {
			t.Errorf("GetAuthUser: want ErrNotFound, got %v", err)
		}
		if _, err := store.GetAuthUserByEmail(ctx, "nobody@example.com"); err != storage.ErrNotFound {
			t.Errorf("GetAuthUserByEmail: want ErrNotFound, got %v", err)
		}
	})

	t.Run("ReplaceGrantsTombstonesRemovedScope", func(t *testing.T) {
		userID := "u2"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: "bob@example.com", Name: "Bob", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}

		if err := store.ReplaceAuthGrants(ctx, userID, []storage.AuthGrant{
			{UserID: userID, Scope: "payments", Role: "editor"},
		}); err != nil {
			t.Fatalf("ReplaceAuthGrants (initial): %v", err)
		}
		got, err := store.ListAuthGrants(ctx, userID)
		if err != nil {
			t.Fatalf("ListAuthGrants: %v", err)
		}
		if len(got) != 1 || got[0].Scope != "payments" || got[0].Role != "editor" {
			t.Fatalf("initial grants wrong: %+v", got)
		}

		// Replace [payments-editor] with [demo-viewer]: payments must be
		// tombstoned (not just left stale), so the live set is demo only.
		if err := store.ReplaceAuthGrants(ctx, userID, []storage.AuthGrant{
			{UserID: userID, Scope: "demo", Role: "viewer"},
		}); err != nil {
			t.Fatalf("ReplaceAuthGrants (replace): %v", err)
		}
		got, err = store.ListAuthGrants(ctx, userID)
		if err != nil {
			t.Fatalf("ListAuthGrants after replace: %v", err)
		}
		if len(got) != 1 || got[0].Scope != "demo" || got[0].Role != "viewer" {
			t.Fatalf("replaced grants wrong (payments scope should be tombstoned): %+v", got)
		}
	})

	t.Run("SessionCreateGetRevoke", func(t *testing.T) {
		userID := "u3"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: "carol@example.com", Name: "Carol", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Millisecond)
		sess := storage.AuthSession{
			TokenHash: "tokenhash-1",
			UserID:    userID,
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
		}
		if err := store.CreateAuthSession(ctx, sess); err != nil {
			t.Fatalf("CreateAuthSession: %v", err)
		}

		got, err := store.GetAuthSession(ctx, "tokenhash-1")
		if err != nil {
			t.Fatalf("GetAuthSession: %v", err)
		}
		if got.UserID != userID {
			t.Errorf("GetAuthSession UserID = %q, want %q", got.UserID, userID)
		}
		if !got.ExpiresAt.Equal(sess.ExpiresAt) {
			t.Errorf("GetAuthSession ExpiresAt = %v, want %v", got.ExpiresAt, sess.ExpiresAt)
		}

		if err := store.RevokeAuthSession(ctx, "tokenhash-1"); err != nil {
			t.Fatalf("RevokeAuthSession: %v", err)
		}

		if _, err := store.GetAuthSession(ctx, "tokenhash-1"); err != storage.ErrNotFound {
			t.Errorf("GetAuthSession after revoke: want ErrNotFound, got %v", err)
		}

		// The migration's TTL safety contract: revocation MUST preserve
		// ExpiresAt (readers filter ExpiresAt > now(), and TTL is keyed off
		// it), so verify the rewritten row still carries the original value.
		var (
			storedExpiresAt time.Time
			revoked         uint8
		)
		row := store.conn.QueryRow(ctx,
			`SELECT ExpiresAt, Revoked FROM auth_session FINAL WHERE TokenHash = ?`, "tokenhash-1")
		if err := row.Scan(&storedExpiresAt, &revoked); err != nil {
			t.Fatalf("querying revoked session row: %v", err)
		}
		if revoked != 1 {
			t.Errorf("Revoked = %d, want 1", revoked)
		}
		if !storedExpiresAt.Equal(sess.ExpiresAt) {
			t.Errorf("ExpiresAt not preserved on revoke: got %v, want %v", storedExpiresAt, sess.ExpiresAt)
		}

		if err := store.RevokeAuthSession(ctx, "no-such-token"); err != storage.ErrNotFound {
			t.Errorf("RevokeAuthSession unknown token: want ErrNotFound, got %v", err)
		}
	})

	t.Run("ExpiredSessionIsErrNotFound", func(t *testing.T) {
		userID := "u4"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: "dave@example.com", Name: "Dave", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Millisecond)
		if err := store.CreateAuthSession(ctx, storage.AuthSession{
			TokenHash: "tokenhash-expired",
			UserID:    userID,
			CreatedAt: now.Add(-2 * time.Hour),
			ExpiresAt: now.Add(-time.Hour), // already expired
		}); err != nil {
			t.Fatalf("CreateAuthSession: %v", err)
		}

		// Pins the revoke-after-expiry contract: an expired (but never
		// revoked) session is indistinguishable from an unknown one, in both
		// directions — get and revoke.
		if _, err := store.GetAuthSession(ctx, "tokenhash-expired"); err != storage.ErrNotFound {
			t.Errorf("GetAuthSession on expired session: want ErrNotFound, got %v", err)
		}
		if err := store.RevokeAuthSession(ctx, "tokenhash-expired"); err != storage.ErrNotFound {
			t.Errorf("RevokeAuthSession on expired session: want ErrNotFound, got %v", err)
		}
	})

	t.Run("DisabledUserRoundtrip", func(t *testing.T) {
		u := storage.AuthUser{
			ID:       "u5",
			Email:    "erin@example.com",
			Name:     "Erin",
			Origin:   "local",
			Disabled: true,
		}
		if err := store.SaveAuthUser(ctx, u); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}

		got, err := store.GetAuthUser(ctx, "u5")
		if err != nil {
			t.Fatalf("GetAuthUser: %v", err)
		}
		if !got.Disabled {
			t.Errorf("Disabled = false, want true (bool<->UInt8 round trip)")
		}

		byEmail, err := store.GetAuthUserByEmail(ctx, "erin@example.com")
		if err != nil {
			t.Fatalf("GetAuthUserByEmail: %v", err)
		}
		if !byEmail.Disabled {
			t.Errorf("GetAuthUserByEmail Disabled = false, want true")
		}
	})

	t.Run("ReplaceGrantsWithNilTombstonesAll", func(t *testing.T) {
		userID := "u6"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: "frank@example.com", Name: "Frank", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}
		if err := store.ReplaceAuthGrants(ctx, userID, []storage.AuthGrant{
			{UserID: userID, Scope: "payments", Role: "editor"},
			{UserID: userID, Scope: "demo", Role: "viewer"},
		}); err != nil {
			t.Fatalf("ReplaceAuthGrants (initial): %v", err)
		}

		// Replacing with nil (revoking every grant, e.g. deactivating a user)
		// must tombstone the whole live set, not no-op.
		if err := store.ReplaceAuthGrants(ctx, userID, nil); err != nil {
			t.Fatalf("ReplaceAuthGrants (nil): %v", err)
		}
		got, err := store.ListAuthGrants(ctx, userID)
		if err != nil {
			t.Fatalf("ListAuthGrants: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ListAuthGrants after nil replace = %+v, want empty", got)
		}
	})

	t.Run("RevokeSessionsForUser", func(t *testing.T) {
		userA := "u8"
		userB := "u9"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userA, Email: "henry@example.com", Name: "Henry", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser userA: %v", err)
		}
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userB, Email: "iris@example.com", Name: "Iris", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser userB: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Millisecond)
		sessions := []storage.AuthSession{
			{TokenHash: "revoke-a1", UserID: userA, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
			{TokenHash: "revoke-a2", UserID: userA, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
			{TokenHash: "revoke-b1", UserID: userB, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		}
		for _, sess := range sessions {
			if err := store.CreateAuthSession(ctx, sess); err != nil {
				t.Fatalf("CreateAuthSession %s: %v", sess.TokenHash, err)
			}
		}

		if err := store.RevokeAuthSessionsForUser(ctx, userA); err != nil {
			t.Fatalf("RevokeAuthSessionsForUser: %v", err)
		}

		if _, err := store.GetAuthSession(ctx, "revoke-a1"); err != storage.ErrNotFound {
			t.Errorf("revoke-a1 after RevokeAuthSessionsForUser: want ErrNotFound, got %v", err)
		}
		if _, err := store.GetAuthSession(ctx, "revoke-a2"); err != storage.ErrNotFound {
			t.Errorf("revoke-a2 after RevokeAuthSessionsForUser: want ErrNotFound, got %v", err)
		}
		got, err := store.GetAuthSession(ctx, "revoke-b1")
		if err != nil {
			t.Fatalf("revoke-b1 (other user) should still resolve: %v", err)
		}
		if got.UserID != userB {
			t.Errorf("revoke-b1 UserID = %q, want %q", got.UserID, userB)
		}
	})

	t.Run("ReplaceGrantsSameScopeRoleChange", func(t *testing.T) {
		userID := "u7"
		if err := store.SaveAuthUser(ctx, storage.AuthUser{ID: userID, Email: "grace@example.com", Name: "Grace", Origin: "local"}); err != nil {
			t.Fatalf("SaveAuthUser: %v", err)
		}
		if err := store.ReplaceAuthGrants(ctx, userID, []storage.AuthGrant{
			{UserID: userID, Scope: "payments", Role: "editor"},
		}); err != nil {
			t.Fatalf("ReplaceAuthGrants (initial): %v", err)
		}

		// Same scope, new role: this is an upsert (no tombstone needed) since
		// the scope stays in the new set — FINAL must still surface the
		// updated role, not the stale one.
		if err := store.ReplaceAuthGrants(ctx, userID, []storage.AuthGrant{
			{UserID: userID, Scope: "payments", Role: "admin"},
		}); err != nil {
			t.Fatalf("ReplaceAuthGrants (role change): %v", err)
		}
		got, err := store.ListAuthGrants(ctx, userID)
		if err != nil {
			t.Fatalf("ListAuthGrants: %v", err)
		}
		if len(got) != 1 || got[0].Scope != "payments" || got[0].Role != "admin" {
			t.Fatalf("role-changed grant wrong: %+v", got)
		}
	})
}

// TestAuthUserOidcGroups pins the OidcGroups Array(String) <-> []string round
// trip: an SSO user's raw IdP groups, captured at login, survive SaveAuthUser
// and a FINAL read back through GetAuthUser.
func TestAuthUserOidcGroups(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	groups := []string{"team-payments", "obs-admins"}
	u := storage.AuthUser{
		ID:         "u10",
		Email:      "judy@example.com",
		Name:       "Judy",
		Origin:     "oidc",
		OidcGroups: groups,
	}
	if err := store.SaveAuthUser(ctx, u); err != nil {
		t.Fatalf("SaveAuthUser: %v", err)
	}

	got, err := store.GetAuthUser(ctx, "u10")
	if err != nil {
		t.Fatalf("GetAuthUser: %v", err)
	}
	if !slices.Equal(got.OidcGroups, groups) {
		t.Errorf("OidcGroups = %v, want %v (Array(String) round trip)", got.OidcGroups, groups)
	}
}
