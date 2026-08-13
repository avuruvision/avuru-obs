package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListOIDCGroupMappings returns the live (non-tombstoned) UI-authored rules,
// by group. FINAL collapses the ReplacingMergeTree to the newest row per
// Group; the table is bounded by how many rules an operator writes, so FINAL
// is cheap (same reasoning as service_group). `Group` is a ClickHouse
// keyword — every statement below backtick-quotes it.
func (s *Store) ListOIDCGroupMappings(ctx context.Context) ([]storage.OIDCGroupMapping, error) {
	rows, err := s.conn.Query(ctx, "SELECT `Group`, Role, Projects, CreatedBy, UpdatedAt FROM oidc_group_mapping FINAL WHERE Deleted = 0 ORDER BY `Group`")
	if err != nil {
		return nil, fmt.Errorf("list oidc group mappings: %w", err)
	}
	defer rows.Close()

	var out []storage.OIDCGroupMapping
	for rows.Next() {
		var m storage.OIDCGroupMapping
		if err := rows.Scan(&m.Group, &m.Role, &m.Projects, &m.CreatedBy, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan oidc group mapping: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveOIDCGroupMapping upserts a rule by Group (new row; ReplacingMergeTree
// keeps the newest by UpdatedAt). CreatedAt/UpdatedAt both default to
// now64(3); saving revives a tombstoned group (Deleted=0).
func (s *Store) SaveOIDCGroupMapping(ctx context.Context, m storage.OIDCGroupMapping) error {
	err := s.conn.Exec(ctx, "INSERT INTO oidc_group_mapping (`Group`, Role, Projects, Deleted, CreatedBy) VALUES (?, ?, ?, 0, ?)",
		m.Group, m.Role, nonNilStrings(m.Projects), m.CreatedBy)
	if err != nil {
		return fmt.Errorf("save oidc group mapping: %w", err)
	}
	return nil
}

// DeleteOIDCGroupMapping tombstones a rule (Deleted=1) so FINAL supersedes
// the live row. ErrNotFound when no live rule has the group.
func (s *Store) DeleteOIDCGroupMapping(ctx context.Context, group string) error {
	var n uint64
	if err := s.conn.QueryRow(ctx, "SELECT count() FROM oidc_group_mapping FINAL WHERE `Group` = ? AND Deleted = 0", group).Scan(&n); err != nil {
		return fmt.Errorf("check oidc group mapping: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	err := s.conn.Exec(ctx, "INSERT INTO oidc_group_mapping (`Group`, Role, Projects, Deleted) VALUES (?, '', [], 1)", group)
	if err != nil {
		return fmt.Errorf("delete oidc group mapping: %w", err)
	}
	return nil
}

// ResetOIDCGroupMappings tombstones every currently-live rule, returning the
// install to exactly what the chart declares. It lists first and inserts a
// tombstone per group rather than a TRUNCATE, so the operation is replicated
// and auditable like every other delete in this table.
func (s *Store) ResetOIDCGroupMappings(ctx context.Context) error {
	live, err := s.ListOIDCGroupMappings(ctx)
	if err != nil {
		return fmt.Errorf("reset oidc group mappings: %w", err)
	}
	for _, m := range live {
		err := s.conn.Exec(ctx, "INSERT INTO oidc_group_mapping (`Group`, Role, Projects, Deleted) VALUES (?, '', [], 1)", m.Group)
		if err != nil {
			return fmt.Errorf("reset oidc group mapping %q: %w", m.Group, err)
		}
	}
	return nil
}
