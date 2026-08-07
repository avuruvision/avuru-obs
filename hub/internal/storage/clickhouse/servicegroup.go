package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListServiceGroups returns the live (non-tombstoned) UI-authored groups, by
// name. FINAL collapses the ReplacingMergeTree to the newest row per Name; the
// table is bounded by how many groups an operator writes, so FINAL is cheap
// (same reasoning as project / alert_channel).
func (s *Store) ListServiceGroups(ctx context.Context) ([]storage.ServiceGroup, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Name, Tier, Namespaces, Services, CreatedBy, CreatedAt, UpdatedAt
FROM service_group FINAL
WHERE Deleted = 0
ORDER BY Name`)
	if err != nil {
		return nil, fmt.Errorf("list service groups: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceGroup
	for rows.Next() {
		var g storage.ServiceGroup
		if err := rows.Scan(&g.Name, &g.Tier, &g.Namespaces, &g.Services, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan service group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveServiceGroup upserts a group by Name (new row; ReplacingMergeTree keeps
// the newest by UpdatedAt). CreatedBy/CreatedAt are written explicitly so an
// edit preserves them; UpdatedAt defaults to now64(3). Saving revives a
// tombstoned name (Deleted=0).
func (s *Store) SaveServiceGroup(ctx context.Context, g storage.ServiceGroup) error {
	err := s.conn.Exec(ctx, `
INSERT INTO service_group (Name, Tier, Namespaces, Services, Deleted, CreatedBy, CreatedAt)
VALUES (?, ?, ?, ?, 0, ?, ?)`,
		g.Name, g.Tier, nonNilStrings(g.Namespaces), nonNilStrings(g.Services), g.CreatedBy, g.CreatedAt)
	if err != nil {
		return fmt.Errorf("save service group: %w", err)
	}
	return nil
}

// DeleteServiceGroup tombstones a group (Deleted=1) so FINAL supersedes the
// live row. ErrNotFound when no live group has the name.
func (s *Store) DeleteServiceGroup(ctx context.Context, name string) error {
	var n uint64
	if err := s.conn.QueryRow(ctx, `
SELECT count() FROM service_group FINAL WHERE Name = ? AND Deleted = 0`, name).Scan(&n); err != nil {
		return fmt.Errorf("check service group: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	err := s.conn.Exec(ctx, `
INSERT INTO service_group (Name, Tier, Namespaces, Services, Deleted) VALUES (?, '', [], [], 1)`, name)
	if err != nil {
		return fmt.Errorf("delete service group: %w", err)
	}
	return nil
}

// nonNilStrings normalizes a nil slice to empty: the driver binds nil as NULL,
// which an Array(String) column rejects.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
