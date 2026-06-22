package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// signalTables maps a public signal name to its main ClickHouse table.
var signalTables = []struct{ signal, table string }{
	{"traces", "otel_traces"},
	{"logs", "otel_logs"},
}

// SystemStats reports per-signal storage usage (system.parts), data freshness
// (min/max Timestamp), and disk capacity (system.disks) for the status view.
func (s *Store) SystemStats(ctx context.Context) (storage.SystemStats, error) {
	var out storage.SystemStats

	sizes, err := s.tableSizes(ctx)
	if err != nil {
		return out, err
	}
	for _, sig := range signalTables {
		st := sizes[sig.table] // zero value when the table has no parts yet
		st.Signal = sig.signal
		oldest, newest, err := s.signalRange(ctx, sig.table)
		if err != nil {
			return out, err
		}
		st.Oldest, st.Newest = oldest, newest
		out.Signals = append(out.Signals, st)
	}

	disks, err := s.disks(ctx)
	if err != nil {
		return out, err
	}
	out.Disks = disks
	return out, nil
}

// tableSizes sums active-part bytes/rows per table in the telemetry database.
func (s *Store) tableSizes(ctx context.Context) (map[string]storage.SignalStats, error) {
	const q = `
SELECT table,
       sum(rows)                    AS rows,
       sum(data_uncompressed_bytes) AS uncompressed,
       sum(data_compressed_bytes)   AS compressed
FROM system.parts
WHERE database = ? AND active
GROUP BY table`
	rows, err := s.conn.Query(ctx, q, s.db)
	if err != nil {
		return nil, fmt.Errorf("table sizes: %w", err)
	}
	defer rows.Close()

	out := map[string]storage.SignalStats{}
	for rows.Next() {
		var (
			table string
			st    storage.SignalStats
		)
		if err := rows.Scan(&table, &st.Rows, &st.Bytes, &st.CompressedBytes); err != nil {
			return nil, fmt.Errorf("scanning table size: %w", err)
		}
		out[table] = st
	}
	return out, rows.Err()
}

// signalRange returns the oldest/newest Timestamp in a table, or nil/nil when
// it is empty (min/max over no rows would otherwise yield the zero time).
func (s *Store) signalRange(ctx context.Context, table string) (oldest, newest *time.Time, err error) {
	q := fmt.Sprintf("SELECT count(), min(Timestamp), max(Timestamp) FROM %s.%s", s.db, table)
	var (
		cnt    uint64
		mn, mx time.Time
	)
	if err := s.conn.QueryRow(ctx, q).Scan(&cnt, &mn, &mx); err != nil {
		return nil, nil, fmt.Errorf("range for %s: %w", table, err)
	}
	if cnt == 0 {
		return nil, nil, nil
	}
	return &mn, &mx, nil
}

// disks reports capacity per ClickHouse storage disk.
func (s *Store) disks(ctx context.Context) ([]storage.DiskStats, error) {
	rows, err := s.conn.Query(ctx, `SELECT name, free_space, total_space FROM system.disks`)
	if err != nil {
		return nil, fmt.Errorf("disks: %w", err)
	}
	defer rows.Close()

	var out []storage.DiskStats
	for rows.Next() {
		var d storage.DiskStats
		if err := rows.Scan(&d.Name, &d.FreeBytes, &d.TotalBytes); err != nil {
			return nil, fmt.Errorf("scanning disk: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
