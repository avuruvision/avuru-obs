package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// CountAuthUsers returns the number of live users (FINAL-collapsed; the table
// is tiny so FINAL is cheap). Used to detect the "no admin yet" bootstrap
// state.
func (s *Store) CountAuthUsers(ctx context.Context) (uint64, error) {
	row := s.conn.QueryRow(ctx, `SELECT count() FROM auth_user FINAL`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("count auth users: %w", err)
	}
	return n, nil
}

// GetAuthUser returns one user by id, or ErrNotFound. Disabled users ARE
// returned — callers decide what disabled means for the request at hand.
func (s *Store) GetAuthUser(ctx context.Context, id string) (storage.AuthUser, error) {
	row := s.conn.QueryRow(ctx, `
SELECT Id, Email, Name, PasswordHash, toString(Origin), Disabled, UpdatedAt
FROM auth_user FINAL
WHERE Id = ?`, id)
	return scanAuthUser(row)
}

// GetAuthUserByEmail returns one user by email, or ErrNotFound. Disabled
// users ARE returned — callers decide.
func (s *Store) GetAuthUserByEmail(ctx context.Context, email string) (storage.AuthUser, error) {
	row := s.conn.QueryRow(ctx, `
SELECT Id, Email, Name, PasswordHash, toString(Origin), Disabled, UpdatedAt
FROM auth_user FINAL
WHERE Email = ?`, email)
	return scanAuthUser(row)
}

// authUserRow is the subset of *sql.Row/driver.Row API scanAuthUser needs.
type authUserRow interface {
	Scan(dest ...any) error
}

func scanAuthUser(row authUserRow) (storage.AuthUser, error) {
	var (
		u        storage.AuthUser
		disabled uint8
	)
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Origin, &disabled, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AuthUser{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AuthUser{}, fmt.Errorf("scan auth user: %w", err)
	}
	u.Disabled = disabled != 0
	return u, nil
}

// ListAuthUsers returns all users (including disabled), ordered by email.
func (s *Store) ListAuthUsers(ctx context.Context) ([]storage.AuthUser, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Id, Email, Name, PasswordHash, toString(Origin), Disabled, UpdatedAt
FROM auth_user FINAL
ORDER BY Email`)
	if err != nil {
		return nil, fmt.Errorf("list auth users: %w", err)
	}
	defer rows.Close()

	var out []storage.AuthUser
	for rows.Next() {
		u, err := scanAuthUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SaveAuthUser upserts a user by Id (new row; ReplacingMergeTree keeps the
// newest by UpdatedAt).
func (s *Store) SaveAuthUser(ctx context.Context, u storage.AuthUser) error {
	var disabled uint8
	if u.Disabled {
		disabled = 1
	}
	err := s.conn.Exec(ctx, `
INSERT INTO auth_user (Id, Email, Name, PasswordHash, Origin, Disabled)
VALUES (?, ?, ?, ?, ?, ?)`, u.ID, u.Email, u.Name, u.PasswordHash, u.Origin, disabled)
	if err != nil {
		return fmt.Errorf("save auth user: %w", err)
	}
	return nil
}

// ListAuthGrants returns a user's live (non-tombstoned) grants, ordered by
// scope.
func (s *Store) ListAuthGrants(ctx context.Context, userID string) ([]storage.AuthGrant, error) {
	rows, err := s.conn.Query(ctx, `
SELECT UserId, Scope, toString(Role)
FROM auth_grant FINAL
WHERE UserId = ? AND Deleted = 0
ORDER BY Scope`, userID)
	if err != nil {
		return nil, fmt.Errorf("list auth grants: %w", err)
	}
	defer rows.Close()

	var out []storage.AuthGrant
	for rows.Next() {
		var g storage.AuthGrant
		if err := rows.Scan(&g.UserID, &g.Scope, &g.Role); err != nil {
			return nil, fmt.Errorf("scan auth grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ReplaceAuthGrants replaces a user's whole grant set: scopes no longer
// present are tombstoned (Deleted=1, empty role) and the new set is written
// live (Deleted=0), all in one batch so a reader never observes a partial
// swap for longer than one insert. Last-writer-wins per scope (by UpdatedAt);
// two concurrent replaces for the same user can interleave and merge their
// live sets rather than one cleanly superseding the other, but admin grant
// edits are effectively single-writer so this is not a practical concern.
func (s *Store) ReplaceAuthGrants(ctx context.Context, userID string, grants []storage.AuthGrant) error {
	existing, err := s.ListAuthGrants(ctx, userID)
	if err != nil {
		return fmt.Errorf("list existing auth grants: %w", err)
	}
	newScopes := make(map[string]struct{}, len(grants))
	for _, g := range grants {
		newScopes[g.Scope] = struct{}{}
	}

	var toTombstone []string
	for _, g := range existing {
		if _, ok := newScopes[g.Scope]; !ok {
			toTombstone = append(toTombstone, g.Scope)
		}
	}
	if len(toTombstone) == 0 && len(grants) == 0 {
		return nil
	}

	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO auth_grant (UserId, Scope, Role, Deleted)")
	if err != nil {
		return fmt.Errorf("prepare auth_grant batch: %w", err)
	}
	for _, scope := range toTombstone {
		if err := batch.Append(userID, scope, "", uint8(1)); err != nil {
			return fmt.Errorf("append auth_grant tombstone: %w", err)
		}
	}
	for _, g := range grants {
		if err := batch.Append(userID, g.Scope, g.Role, uint8(0)); err != nil {
			return fmt.Errorf("append auth_grant: %w", err)
		}
	}
	return batch.Send()
}

// CreateAuthSession inserts a new, live session. Uses a batch (rather than a
// bound Exec) so the DateTime64(3) CreatedAt/ExpiresAt values round-trip at
// millisecond precision (session expiry math needs sub-second accuracy).
func (s *Store) CreateAuthSession(ctx context.Context, sess storage.AuthSession) error {
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO auth_session (TokenHash, UserId, CreatedAt, ExpiresAt, Revoked)")
	if err != nil {
		return fmt.Errorf("prepare auth_session batch: %w", err)
	}
	if err := batch.Append(sess.TokenHash, sess.UserID, sess.CreatedAt, sess.ExpiresAt, uint8(0)); err != nil {
		return fmt.Errorf("append auth_session: %w", err)
	}
	return batch.Send()
}

// GetAuthSession returns a session, or ErrNotFound if the token is unknown,
// revoked, or past its ExpiresAt.
func (s *Store) GetAuthSession(ctx context.Context, tokenHash string) (storage.AuthSession, error) {
	sess := storage.AuthSession{TokenHash: tokenHash}
	err := s.conn.QueryRow(ctx, `
SELECT UserId, CreatedAt, ExpiresAt
FROM auth_session FINAL
WHERE TokenHash = ? AND Revoked = 0 AND ExpiresAt > now64(3)`, tokenHash).
		Scan(&sess.UserID, &sess.CreatedAt, &sess.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AuthSession{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AuthSession{}, fmt.Errorf("get auth session: %w", err)
	}
	return sess, nil
}

// RevokeAuthSession tombstones a session (Revoked=1), preserving CreatedAt and
// ExpiresAt — the migration's TTL safety contract requires ExpiresAt survive
// the rewrite so the table's TTL (ExpiresAt + 7d) still fires. Uses a batch
// (rather than a bound Exec) for the same DateTime64(3) precision reason as
// CreateAuthSession. Returns ErrNotFound (via GetAuthSession) if the token is
// unknown, already revoked, or expired.
func (s *Store) RevokeAuthSession(ctx context.Context, tokenHash string) error {
	sess, err := s.GetAuthSession(ctx, tokenHash)
	if err != nil {
		return err
	}
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO auth_session (TokenHash, UserId, CreatedAt, ExpiresAt, Revoked)")
	if err != nil {
		return fmt.Errorf("prepare auth_session batch: %w", err)
	}
	if err := batch.Append(tokenHash, sess.UserID, sess.CreatedAt, sess.ExpiresAt, uint8(1)); err != nil {
		return fmt.Errorf("append auth_session revoke: %w", err)
	}
	return batch.Send()
}

// RevokeAuthSessionsForUser revokes every live (unexpired, not already
// revoked) session for userID in one batch — used on password rotation, so a
// compromised account's existing cookies stop working the moment the
// password changes, rather than surviving until each session's own TTL. Each
// rewritten row preserves its own CreatedAt/ExpiresAt, the same TTL safety
// contract documented on RevokeAuthSession: the table's TTL is keyed off
// ExpiresAt, so a tombstone that changed it would let the row outlive (or
// die before) the original session's TTL. A no-op when the user has no live
// sessions.
func (s *Store) RevokeAuthSessionsForUser(ctx context.Context, userID string) error {
	rows, err := s.conn.Query(ctx, `
SELECT TokenHash, CreatedAt, ExpiresAt
FROM auth_session FINAL
WHERE UserId = ? AND Revoked = 0 AND ExpiresAt > now64(3)`, userID)
	if err != nil {
		return fmt.Errorf("list live auth sessions: %w", err)
	}
	defer rows.Close()

	type liveSession struct {
		tokenHash string
		createdAt time.Time
		expiresAt time.Time
	}
	var live []liveSession
	for rows.Next() {
		var ls liveSession
		if err := rows.Scan(&ls.tokenHash, &ls.createdAt, &ls.expiresAt); err != nil {
			return fmt.Errorf("scan auth session: %w", err)
		}
		live = append(live, ls)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(live) == 0 {
		return nil
	}

	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO auth_session (TokenHash, UserId, CreatedAt, ExpiresAt, Revoked)")
	if err != nil {
		return fmt.Errorf("prepare auth_session batch: %w", err)
	}
	for _, ls := range live {
		if err := batch.Append(ls.tokenHash, userID, ls.createdAt, ls.expiresAt, uint8(1)); err != nil {
			return fmt.Errorf("append auth_session revoke: %w", err)
		}
	}
	return batch.Send()
}
