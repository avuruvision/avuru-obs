package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TraceHeatmap buckets spans into a (time × log2-duration) histogram. With a
// service filter it buckets that service's entry spans (Server/Consumer) so a
// non-root service still gets a heatmap, and brushing a band coheres with the
// participant-filtered trace list (which also judges durations on that
// service's spans). Without a filter it buckets root spans.
//
// TODO(orphans): the unfiltered case still keys on parentless spans while
// SearchTraces lists effective-root (incl. orphaned) traces, so brushing a
// region can select fewer traces than the list shows. Aligning it means a
// per-trace-grouping rewrite with its own duration-bucket semantics —
// deferred as a fast-follow.
func (s *Store) TraceHeatmap(ctx context.Context, q storage.HeatmapQuery) (storage.Heatmap, error) {
	timeBuckets := q.TimeBuckets
	if timeBuckets <= 0 || timeBuckets > 240 {
		timeBuckets = 60
	}
	durBuckets := q.DurationBuckets
	if durBuckets <= 0 || durBuckets > 40 {
		durBuckets = 24
	}

	bucketSec := int64(q.Range.End.Sub(q.Range.Start).Seconds()) / int64(timeBuckets)
	if bucketSec < 1 {
		bucketSec = 1
	}

	query := `
SELECT
    toInt32(intDiv(toUnixTimestamp(Timestamp) - ?, ?))                                  AS t,
    toInt32(least(greatest(floor(log2(greatest(Duration / 1000000, 1))), 0), ? - 1))    AS d,
    count()                                                                              AS c,
    countIf(` + errorSpanExpr("") + `)                                                   AS e
FROM otel_traces
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?`
	args := []any{q.Range.Start.Unix(), bucketSec, durBuckets, q.Tenant, q.Range.Start, q.Range.End}
	if q.Service != "" {
		query += ` AND ServiceName = ? AND SpanKind IN ('Server', 'Consumer')`
		args = append(args, q.Service)
		if q.Operation != "" {
			query += ` AND SpanName = ?`
			args = append(args, q.Operation)
		}
	} else {
		query += ` AND ParentSpanId = ''`
		if q.Operation != "" {
			query += ` AND SpanName = ?`
			args = append(args, q.Operation)
		}
	}
	query, args = tagFilters(query, q.Tags, args)
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
GROUP BY t, d
ORDER BY t, d`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.Heatmap{}, fmt.Errorf("trace heatmap: %w", err)
	}
	defer rows.Close()

	hm := storage.Heatmap{
		TimeBucket:     time.Duration(bucketSec) * time.Second,
		DurationBounds: make([]time.Duration, durBuckets),
	}
	for i := 0; i < durBuckets; i++ {
		hm.DurationBounds[i] = time.Duration(1<<uint(i)) * time.Millisecond
	}
	for rows.Next() {
		var cell storage.HeatmapCell
		var t, d int32
		if err := rows.Scan(&t, &d, &cell.Count, &cell.ErrorCount); err != nil {
			return storage.Heatmap{}, fmt.Errorf("scanning heatmap cell: %w", err)
		}
		cell.TimeBucket, cell.DurationBucket = int(t), int(d)
		// Clamp the edge cell produced when Timestamp == Range.End boundary
		// rounding lands on bucket index == timeBuckets.
		if cell.TimeBucket >= timeBuckets {
			cell.TimeBucket = timeBuckets - 1
		}
		hm.Cells = append(hm.Cells, cell)
	}
	return hm, rows.Err()
}
