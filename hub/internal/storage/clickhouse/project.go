package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListProjects returns the live (non-tombstoned) UI-managed projects, by id.
// FINAL collapses the ReplacingMergeTree to the newest row per Id; the table is
// bounded by project count so FINAL is cheap (same reasoning as alert_channel).
func (s *Store) ListProjects(ctx context.Context) ([]storage.Project, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Id, Label, Members, RetentionDays, CreatedBy, CreatedAt, UpdatedAt
FROM project FINAL
WHERE Deleted = 0
ORDER BY Id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []storage.Project
	for rows.Next() {
		var p storage.Project
		// RetentionDays is UInt16 on the wire; the driver will not scan it into
		// an int, so it lands in its own variable (same for GetProject).
		var retention uint16
		if err := rows.Scan(&p.ID, &p.Label, &p.Members, &retention, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		p.RetentionDays = int(retention)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one live project by id, or ErrNotFound (absent or deleted).
func (s *Store) GetProject(ctx context.Context, id string) (storage.Project, error) {
	var p storage.Project
	var retention uint16
	err := s.conn.QueryRow(ctx, `
SELECT Id, Label, Members, RetentionDays, CreatedBy, CreatedAt, UpdatedAt
FROM project FINAL
WHERE Id = ? AND Deleted = 0`, id).
		Scan(&p.ID, &p.Label, &p.Members, &retention, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.Project{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Project{}, fmt.Errorf("get project: %w", err)
	}
	p.RetentionDays = int(retention)
	return p, nil
}

// SaveProject upserts a project by Id (new row; ReplacingMergeTree keeps the
// newest by UpdatedAt). CreatedBy/CreatedAt are written explicitly so a rename
// (re-save) preserves them; UpdatedAt defaults to now64(3). Saving revives a
// tombstoned id (Deleted=0).
func (s *Store) SaveProject(ctx context.Context, p storage.Project) error {
	members := p.Members
	if members == nil {
		members = []string{}
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, RetentionDays, Deleted, CreatedBy, CreatedAt)
VALUES (?, ?, ?, ?, 0, ?, ?)`, p.ID, p.Label, members, uint16(retentionDays(p)), p.CreatedBy, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// DeleteProject tombstones a project (Deleted=1) so FINAL supersedes the live
// row. ErrNotFound when no live project has the id.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	var n uint64
	if err := s.conn.QueryRow(ctx, `
SELECT count() FROM project FINAL WHERE Id = ? AND Deleted = 0`, id).Scan(&n); err != nil {
		return fmt.Errorf("check project: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, RetentionDays, Deleted) VALUES (?, '', [], 0, 1)`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// retentionDays clamps a project's retention into the UInt16 column. Negative
// is treated as 0 (inherit the global window) rather than wrapping to 65535 —
// a validation slip must never turn into "keep this project for 179 years".
func retentionDays(p storage.Project) int {
	if p.RetentionDays < 0 {
		return 0
	}
	if p.RetentionDays > 65535 {
		return 65535
	}
	return p.RetentionDays
}
