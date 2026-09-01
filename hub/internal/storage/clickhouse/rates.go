package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// LoadRatesOverlay returns the UI-authored rate table, or storage.ErrNotFound
// if none has ever been saved. FINAL collapses the ReplacingMergeTree to the
// single row; the table is a deliberate singleton (Id is always 'default') so
// FINAL is cheap. Same shape as the collection overlay, deliberately — one
// idiom for "one mutable document the UI owns".
func (s *Store) LoadRatesOverlay(ctx context.Context) (storage.RatesOverlay, error) {
	rows, err := s.conn.Query(ctx, `
SELECT Overlay, UpdatedAt, UpdatedBy
FROM rates_overlay FINAL
WHERE Id = 'default'`)
	if err != nil {
		return storage.RatesOverlay{}, fmt.Errorf("load rates overlay: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return storage.RatesOverlay{}, storage.ErrNotFound
	}
	var ov storage.RatesOverlay
	if err := rows.Scan(&ov.Overlay, &ov.UpdatedAt, &ov.UpdatedBy); err != nil {
		return storage.RatesOverlay{}, fmt.Errorf("scan rates overlay: %w", err)
	}
	return ov, rows.Err()
}

// SaveRatesOverlay upserts the singleton row; ReplacingMergeTree keeps the
// newest write (UpdatedAt, defaulted server-side to now64(3)).
func (s *Store) SaveRatesOverlay(ctx context.Context, ov storage.RatesOverlay) error {
	err := s.conn.Exec(ctx, `
INSERT INTO rates_overlay (Id, Overlay, UpdatedBy)
VALUES ('default', ?, ?)`, ov.Overlay, ov.UpdatedBy)
	if err != nil {
		return fmt.Errorf("save rates overlay: %w", err)
	}
	return nil
}
