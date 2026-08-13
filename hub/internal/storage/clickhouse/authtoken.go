package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// chEpoch is what a zero time.Time becomes on the way into a DateTime64(3)
// column, and what comes back out as a zero time.Time on the way in. Go's zero
// time is year 1, which DateTime64 cannot represent (its range starts in 1900),
// so "never expires" / "never used" is stored as the epoch — the same value the
// migration's DEFAULT toDateTime64(0, 3) writes — and translated at the edge.
// Without this the driver would either error or clamp, and a token meant to
// never expire could come back looking expired.
var chEpoch = time.Unix(0, 0).UTC()

// toCH also TRUNCATES to the second, deliberately. The columns are
// DateTime64(3), but these inserts bind their arguments the way every other
// UI-authored table here does, and that path lands at second granularity — so
// a millisecond written is a millisecond silently dropped. Truncating here
// makes the Go value and the stored value agree instead of quietly disagreeing.
//
// Truncation (not rounding) is the safe direction for ExpiresAt: rounding up
// would let a token outlive the instant its owner was told it expires.
// Second granularity is ample for both fields — an expiry is set in days, and
// LastUsedAt is debounced to once a minute by design.
func toCH(t time.Time) time.Time {
	if t.IsZero() {
		return chEpoch
	}
	return t.Truncate(time.Second)
}

func fromCH(t time.Time) time.Time {
	if t.IsZero() || t.Equal(chEpoch) || t.Unix() <= 0 {
		return time.Time{}
	}
	return t
}

// CreateAuthToken inserts a live personal API token. The raw token is never
// stored — only TokenHash — so this row is metadata plus a lookup key.
// ReplacingMergeTree keys on TokenHash, so a revoke is a newer row with
// Revoked=1 (see RevokeAuthToken) and a touch is a newer row with a fresher
// LastUsedAt (see TouchAuthToken).
func (s *Store) CreateAuthToken(ctx context.Context, t storage.AuthToken) error {
	err := s.conn.Exec(ctx, `
INSERT INTO auth_token (TokenHash, UserID, Name, Prefix, ExpiresAt, LastUsedAt, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		t.TokenHash, t.UserID, t.Name, t.Prefix,
		toCH(t.ExpiresAt), toCH(t.LastUsedAt), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}
	return nil
}

// GetAuthTokenByHash returns one live token by its hash, or ErrNotFound when the
// hash is unknown OR the token has been revoked.
//
// An EXPIRED token is returned normally. Expiry is decided one layer up, in the
// auth package: a caller presenting an expired token must get the same 401 as a
// revoked one, but its owner opening Settings must still see the row and read
// the expiry date, which is the whole explanation for why their script broke.
// Filtering it out here would make that impossible.
//
// FINAL collapses the ReplacingMergeTree to the newest row per TokenHash; the
// table is bounded by token count so FINAL is cheap (same reasoning as
// auth_ingest_key / project).
func (s *Store) GetAuthTokenByHash(ctx context.Context, tokenHash string) (storage.AuthToken, error) {
	var t storage.AuthToken
	err := s.conn.QueryRow(ctx, `
SELECT TokenHash, UserID, Name, Prefix, ExpiresAt, LastUsedAt, CreatedAt
FROM auth_token FINAL
WHERE TokenHash = ? AND Revoked = 0`, tokenHash).
		Scan(&t.TokenHash, &t.UserID, &t.Name, &t.Prefix, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AuthToken{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AuthToken{}, fmt.Errorf("get auth token: %w", err)
	}
	t.ExpiresAt = fromCH(t.ExpiresAt)
	t.LastUsedAt = fromCH(t.LastUsedAt)
	return t, nil
}

// ListAuthTokens returns the live tokens owned by one user, newest first,
// including expired ones — see GetAuthTokenByHash for why. The raw token is
// never stored, so callers only ever see the prefix + metadata.
func (s *Store) ListAuthTokens(ctx context.Context, userID string) ([]storage.AuthToken, error) {
	rows, err := s.conn.Query(ctx, `
SELECT TokenHash, UserID, Name, Prefix, ExpiresAt, LastUsedAt, CreatedAt
FROM auth_token FINAL
WHERE UserID = ? AND Revoked = 0
ORDER BY CreatedAt DESC, TokenHash`, userID)
	if err != nil {
		return nil, fmt.Errorf("list auth tokens: %w", err)
	}
	defer rows.Close()

	var out []storage.AuthToken
	for rows.Next() {
		var t storage.AuthToken
		if err := rows.Scan(&t.TokenHash, &t.UserID, &t.Name, &t.Prefix,
			&t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan auth token: %w", err)
		}
		t.ExpiresAt = fromCH(t.ExpiresAt)
		t.LastUsedAt = fromCH(t.LastUsedAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAuthToken tombstones a token (Revoked=1) so FINAL supersedes the live
// row. The owner scope guards against revoking another user's token by hash —
// the same guard RevokeIngestKey applies with its project. ErrNotFound when no
// live token with that hash belongs to that user.
func (s *Store) RevokeAuthToken(ctx context.Context, userID, tokenHash string) error {
	var (
		n    uint64
		name string
	)
	err := s.conn.QueryRow(ctx, `
SELECT count(), any(Name) FROM auth_token FINAL
WHERE TokenHash = ? AND UserID = ? AND Revoked = 0`, tokenHash, userID).Scan(&n, &name)
	if err != nil {
		return fmt.Errorf("check auth token: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	// Preserve identifying metadata on the tombstone so a later list/get still
	// collapses to a consistent (revoked) record.
	err = s.conn.Exec(ctx, `
INSERT INTO auth_token (TokenHash, UserID, Name, Revoked) VALUES (?, ?, ?, 1)`,
		tokenHash, userID, name)
	if err != nil {
		return fmt.Errorf("revoke auth token: %w", err)
	}
	return nil
}

// TouchAuthToken records that a token was just used, as a ReplacingMergeTree
// upsert rather than an ALTER.
//
// It re-inserts the WHOLE row, not just the changed column: FINAL keeps the
// newest row per key, not a column-wise merge of them, so inserting
// (TokenHash, LastUsedAt) alone would blank Name, Prefix, ExpiresAt and
// CreatedAt on the next collapse — the token would keep working while quietly
// losing its expiry. Hence the read-then-write.
//
// Callers debounce this; storage does not. Writing it per request would mean
// one INSERT per API call for exactly the traffic tokens exist to enable.
func (s *Store) TouchAuthToken(ctx context.Context, tokenHash string, at time.Time) error {
	cur, err := s.GetAuthTokenByHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	err = s.conn.Exec(ctx, `
INSERT INTO auth_token (TokenHash, UserID, Name, Prefix, ExpiresAt, LastUsedAt, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		cur.TokenHash, cur.UserID, cur.Name, cur.Prefix,
		toCH(cur.ExpiresAt), toCH(at), cur.CreatedAt)
	if err != nil {
		return fmt.Errorf("touch auth token: %w", err)
	}
	return nil
}
