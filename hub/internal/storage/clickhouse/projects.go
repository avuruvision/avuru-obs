package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListProjects returns live (non-tombstoned) UI-managed projects, ordered by Id.
func (s *Store) ListProjects(ctx context.Context) ([]storage.Project, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Id, Label, Members, CreatedBy, CreatedAt, UpdatedAt
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
		if err := rows.Scan(&p.ID, &p.Label, &p.Members, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one live project by Id, or ErrNotFound.
func (s *Store) GetProject(ctx context.Context, id string) (storage.Project, error) {
	var p storage.Project
	err := s.conn.QueryRow(ctx, `
SELECT Id, Label, Members, CreatedBy, CreatedAt, UpdatedAt
FROM project FINAL
WHERE Id = ? AND Deleted = 0`, id).
		Scan(&p.ID, &p.Label, &p.Members, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.Project{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// SaveProject upserts by Id (new row; ReplacingMergeTree keeps the newest by
// UpdatedAt). Members defaults to an empty array when nil.
func (s *Store) SaveProject(ctx context.Context, p storage.Project) error {
	members := p.Members
	if members == nil {
		members = []string{}
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, CreatedBy, Deleted)
VALUES (?, ?, ?, ?, 0)`, p.ID, p.Label, members, p.CreatedBy)
	if err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// DeleteProject tombstones by Id (Deleted=1, newer UpdatedAt). Returns
// ErrNotFound when no live row matches.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	if _, err := s.GetProject(ctx, id); err != nil {
		return err // ErrNotFound propagates
	}
	err := s.conn.Exec(ctx, `
INSERT INTO project (Id, Label, Members, Deleted)
VALUES (?, '', [], 1)`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}
