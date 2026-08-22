package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ingestRateWindow is how far back the per-project ingest rate is measured.
// An hour is long enough that a bursty sender does not read as idle, and short
// enough that "still sending?" answers about now rather than about yesterday.
const ingestRateWindow = time.Hour

// TenantUsage reports one project's share of the store, per signal. It reuses
// signalTables — the same signal→tables mapping the instance-wide status uses —
// so the two halves of the Status page can never disagree about what a signal
// is made of.
//
// Rows, freshness and the ingest rate are exact: they are counted with the
// tenant filter. Bytes are NOT — ClickHouse parts hold every tenant's rows
// together, so a project's share of a table's compressed bytes can only be
// apportioned by row count. That is why the field is named EstimatedBytes and
// is labeled an estimate everywhere it surfaces.
func (s *Store) TenantUsage(ctx context.Context, tenants []string, now time.Time) (storage.TenantUsage, error) {
	var out storage.TenantUsage
	if len(tenants) == 0 {
		return out, fmt.Errorf("tenant usage: no tenants")
	}
	existing, err := s.existingTables(ctx)
	if err != nil {
		return out, err
	}
	// Table totals come from system.parts, exactly like the instance-wide view,
	// and are only used as the denominator of the byte apportionment.
	sizes, err := s.tableSizes(ctx)
	if err != nil {
		return out, err
	}

	since := now.Add(-ingestRateWindow)
	for _, sig := range signalTables {
		var u storage.TenantSignalUsage
		u.Signal = sig.signal
		present := false
		var recentRows uint64
		for _, table := range sig.tables {
			if !existing[table] {
				continue
			}
			present = true
			rows, oldest, newest, recent, err := s.tenantTableUsage(ctx, table, sig.timeCol, tenants, since)
			if err != nil {
				return out, err
			}
			u.Rows += rows
			recentRows += recent
			if sz := sizes[table]; rows > 0 && sz.Rows > 0 {
				// Apportion by row share. Integer math on uint64 would round a
				// small tenant to zero, so the ratio is computed in float and
				// converted once.
				u.EstimatedBytes += uint64(float64(sz.CompressedBytes) * (float64(rows) / float64(sz.Rows)))
			}
			if oldest != nil && (u.Oldest == nil || oldest.Before(*u.Oldest)) {
				u.Oldest = oldest
			}
			if newest != nil && (u.Newest == nil || newest.After(*u.Newest)) {
				u.Newest = newest
			}
		}
		if !present {
			continue // module off: its migration never ran
		}
		u.RowsPerMinute = float64(recentRows) / ingestRateWindow.Minutes()
		out.Signals = append(out.Signals, u)
	}
	return out, nil
}

// tenantTableUsage counts one table for the tenant set: total rows, the time
// bounds, and the rows written since `since` (the ingest rate's numerator).
// One pass, so a signal costs one query per table rather than four.
func (s *Store) tenantTableUsage(ctx context.Context, table, timeCol string, tenants []string, since time.Time) (rows uint64, oldest, newest *time.Time, recent uint64, err error) {
	q := fmt.Sprintf(`
SELECT count(),
       min(%[1]s),
       max(%[1]s),
       countIf(%[1]s >= ?)
FROM %[2]s.%[3]s
WHERE Tenant IN (?)`, timeCol, s.db, table)
	var count, recentCount uint64
	var minTS, maxTS time.Time
	if err := s.conn.QueryRow(ctx, q, since, tenants).Scan(&count, &minTS, &maxTS, &recentCount); err != nil {
		return 0, nil, nil, 0, fmt.Errorf("tenant usage on %s: %w", table, err)
	}
	if count == 0 {
		// min()/max() over no rows return the epoch, which would render as
		// "oldest data: 1970" — a project with no data has no bounds at all.
		return 0, nil, nil, 0, nil
	}
	o, n := minTS.UTC(), maxTS.UTC()
	return count, &o, &n, recentCount, nil
}
