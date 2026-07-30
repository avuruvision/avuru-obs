package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// CreateIngestKey inserts a live per-project ingest key. CreatedBy/CreatedAt are
// written explicitly (the API layer stamps them); Revoked defaults to 0 and
// UpdatedAt to now64(3). ReplacingMergeTree keys on KeyHash, so a revoke is a
// newer row with Revoked=1 (see RevokeIngestKey).
func (s *Store) CreateIngestKey(ctx context.Context, k storage.AuthIngestKey) error {
	err := s.conn.Exec(ctx, `
INSERT INTO auth_ingest_key (KeyHash, Project, Name, Prefix, CreatedBy, Revoked, CreatedAt)
VALUES (?, ?, ?, ?, ?, 0, ?)`, k.KeyHash, k.Project, k.Name, k.Prefix, k.CreatedBy, k.CreatedAt)
	if err != nil {
		return fmt.Errorf("create ingest key: %w", err)
	}
	return nil
}

// GetIngestKeyByHash returns one live key by its hash, or ErrNotFound when the
// hash is unknown OR the key has been revoked. FINAL collapses the
// ReplacingMergeTree to the newest row per KeyHash; the table is bounded by key
// count so FINAL is cheap (same reasoning as project / alert_channel).
func (s *Store) GetIngestKeyByHash(ctx context.Context, keyHash string) (storage.AuthIngestKey, error) {
	var k storage.AuthIngestKey
	err := s.conn.QueryRow(ctx, `
SELECT KeyHash, Project, Name, Prefix, CreatedBy, CreatedAt
FROM auth_ingest_key FINAL
WHERE KeyHash = ? AND Revoked = 0`, keyHash).
		Scan(&k.KeyHash, &k.Project, &k.Name, &k.Prefix, &k.CreatedBy, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.AuthIngestKey{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.AuthIngestKey{}, fmt.Errorf("get ingest key: %w", err)
	}
	return k, nil
}

// ListIngestKeys returns the live keys for one project, newest first. The raw
// key is never stored, so callers only ever see the prefix + metadata.
func (s *Store) ListIngestKeys(ctx context.Context, project string) ([]storage.AuthIngestKey, error) {
	rows, err := s.conn.Query(ctx, `
SELECT KeyHash, Project, Name, Prefix, CreatedBy, CreatedAt
FROM auth_ingest_key FINAL
WHERE Project = ? AND Revoked = 0
ORDER BY CreatedAt DESC, KeyHash`, project)
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	defer rows.Close()

	var out []storage.AuthIngestKey
	for rows.Next() {
		var k storage.AuthIngestKey
		if err := rows.Scan(&k.KeyHash, &k.Project, &k.Name, &k.Prefix, &k.CreatedBy, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ingest key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeIngestKey tombstones a key (Revoked=1) so FINAL supersedes the live row.
// The project scope guards against revoking another project's key by hash.
// ErrNotFound when no live key with that hash exists in the project.
func (s *Store) RevokeIngestKey(ctx context.Context, project, keyHash string) error {
	var (
		n    uint64
		name string
	)
	err := s.conn.QueryRow(ctx, `
SELECT count(), any(Name) FROM auth_ingest_key FINAL
WHERE KeyHash = ? AND Project = ? AND Revoked = 0`, keyHash, project).Scan(&n, &name)
	if err != nil {
		return fmt.Errorf("check ingest key: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	// Preserve identifying metadata on the tombstone row so a later list/get
	// still collapses to a consistent (revoked) record.
	err = s.conn.Exec(ctx, `
INSERT INTO auth_ingest_key (KeyHash, Project, Name, Revoked) VALUES (?, ?, ?, 1)`,
		keyHash, project, name)
	if err != nil {
		return fmt.Errorf("revoke ingest key: %w", err)
	}
	return nil
}
