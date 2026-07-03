package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const (
	defaultREDPoints = 30
	defaultREDTopN   = 6
)

// REDSeries buckets RED stats over entry spans (SpanKind Server/Consumer —
// the same population as ListServices) per service. Empty q.Service returns
// the TopN busiest services of the range.
func (s *Store) REDSeries(ctx context.Context, q storage.REDQuery) ([]storage.REDSeries, error) {
	points := q.Points
	if points <= 0 {
		points = defaultREDPoints
	}
	bucket := q.Range.End.Sub(q.Range.Start) / time.Duration(points)
	if bucket < time.Second {
		bucket = time.Second
	}
	topN := q.TopN
	if topN <= 0 {
		topN = defaultREDTopN
	}

	query := fmt.Sprintf(`
SELECT
    ServiceName,
    toStartOfInterval(Timestamp, INTERVAL %d SECOND)  AS t,
    count()                                           AS reqs,
    countIf(StatusCode = 'Error')                     AS errors,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration))   AS qs
FROM otel_traces
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?
  AND SpanKind IN ('Server', 'Consumer')`, int(bucket.Seconds()))
	args := []any{q.Tenant, q.Range.Start, q.Range.End}

	if q.Service != "" {
		query += ` AND ServiceName = ?`
		args = append(args, q.Service)
	} else {
		// Busiest services of the same range and population.
		sub := `
  AND ServiceName IN (
    SELECT ServiceName FROM otel_traces
    WHERE Tenant = ? AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind IN ('Server', 'Consumer')`
		subArgs := []any{q.Tenant, q.Range.Start, q.Range.End}
		if q.ExcludeAux {
			sub += auxExclusion("")
		}
		sub += fmt.Sprintf(`
    GROUP BY ServiceName ORDER BY count() DESC LIMIT %d)`, topN)
		query += sub
		args = append(args, subArgs...)
	}
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
GROUP BY ServiceName, t
ORDER BY ServiceName, t`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("red series: %w", err)
	}
	defer rows.Close()

	var (
		out []storage.REDSeries
		cur *storage.REDSeries
	)
	for rows.Next() {
		var (
			service string
			t       time.Time
			reqs    uint64
			errs    uint64
			quant   []float64
		)
		if err := rows.Scan(&service, &t, &reqs, &errs, &quant); err != nil {
			return nil, fmt.Errorf("scanning red row: %w", err)
		}
		if cur == nil || cur.Service != service {
			out = append(out, storage.REDSeries{Service: service})
			cur = &out[len(out)-1]
		}
		p50, p95, p99 := nsQuantiles(quant)
		cur.Points = append(cur.Points, storage.REDPoint{
			Time: t, Count: reqs, ErrorCount: errs, P50: p50, P95: p95, P99: p99,
		})
	}
	return out, rows.Err()
}
