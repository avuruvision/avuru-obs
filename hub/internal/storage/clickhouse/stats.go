package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// signalTables maps a public signal name to its ClickHouse tables and their
// event-time column (metrics tables use TimeUnix, not Timestamp).
var signalTables = []struct {
	signal  string
	tables  []string
	timeCol string
}{
	{"traces", []string{"otel_traces"}, "Timestamp"},
	{"logs", []string{"otel_logs"}, "Timestamp"},
	{"metrics", metricsTables, "TimeUnix"},
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
		var st storage.SignalStats
		st.Signal = sig.signal
		for _, table := range sig.tables {
			sz := sizes[table] // zero value when the table has no parts yet
			st.Rows += sz.Rows
			st.Bytes += sz.Bytes
			st.CompressedBytes += sz.CompressedBytes
			oldest, newest, err := s.signalRange(ctx, table, sig.timeCol)
			if err != nil {
				return out, err
			}
			if oldest != nil && (st.Oldest == nil || oldest.Before(*st.Oldest)) {
				st.Oldest = oldest
			}
			if newest != nil && (st.Newest == nil || newest.After(*st.Newest)) {
				st.Newest = newest
			}
		}
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

// signalRange returns the oldest/newest event time in a table, or nil/nil when
// it is empty (min/max over no rows would otherwise yield the zero time).
func (s *Store) signalRange(ctx context.Context, table, timeCol string) (oldest, newest *time.Time, err error) {
	q := fmt.Sprintf("SELECT count(), min(%s), max(%s) FROM %s.%s", timeCol, timeCol, s.db, table)
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
