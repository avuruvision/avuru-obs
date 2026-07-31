package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// LoadCollectionOverlay returns the current overlay, or storage.ErrNotFound
// if none has ever been saved. FINAL collapses the ReplacingMergeTree to the
// single row; the table is a deliberate singleton (Id is always 'default')
// so FINAL is cheap.
func (s *Store) LoadCollectionOverlay(ctx context.Context) (storage.CollectionOverlay, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Overlay, UpdatedAt, UpdatedBy
FROM collection_overlay FINAL
WHERE Id = 'default'`)
	if err != nil {
		return storage.CollectionOverlay{}, fmt.Errorf("load collection overlay: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return storage.CollectionOverlay{}, storage.ErrNotFound
	}
	var ov storage.CollectionOverlay
	if err := rows.Scan(&ov.Overlay, &ov.UpdatedAt, &ov.UpdatedBy); err != nil {
		return storage.CollectionOverlay{}, fmt.Errorf("scan collection overlay: %w", err)
	}
	return ov, rows.Err()
}

// SaveCollectionOverlay upserts the singleton row; ReplacingMergeTree keeps
// the newest write (UpdatedAt, defaulted server-side to now64(3)).
func (s *Store) SaveCollectionOverlay(ctx context.Context, ov storage.CollectionOverlay) error {
	err := s.conn.Exec(ctx, `
INSERT INTO collection_overlay (Id, Overlay, UpdatedBy)
VALUES ('default', ?, ?)`, ov.Overlay, ov.UpdatedBy)
	if err != nil {
		return fmt.Errorf("save collection overlay: %w", err)
	}
	return nil
}
