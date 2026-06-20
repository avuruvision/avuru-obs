package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListServices aggregates RED stats per service over entry spans
// (SpanKind Server/Consumer — the request-handling side).
func (s *Store) ListServices(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceStats, error) {
	const query = `
SELECT
    ServiceName,
    count()                                         AS spans,
    countIf(StatusCode = 'Error')                   AS errors,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?
  AND SpanKind IN ('Server', 'Consumer')
GROUP BY ServiceName
ORDER BY spans DESC`

	rows, err := s.conn.Query(ctx, query, q.Tenant, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceStats
	for rows.Next() {
		var (
			st    storage.ServiceStats
			quant []float64
		)
		if err := rows.Scan(&st.Name, &st.SpanCount, &st.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning service row: %w", err)
		}
		st.P50, st.P95, st.P99 = nsQuantiles(quant)
		out = append(out, st)
	}
	return out, rows.Err()
}

// TraceOverview aggregates RED stats per (service, operation) over root spans.
func (s *Store) TraceOverview(ctx context.Context, q storage.OverviewQuery) ([]storage.OperationStats, error) {
	query := `
SELECT
    ServiceName,
    SpanName,
    count()                                         AS reqs,
    countIf(StatusCode = 'Error')                   AS errors,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?
  AND ParentSpanId = ''`
	args := []any{q.Tenant, q.Range.Start, q.Range.End}
	if q.Service != "" {
		query += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	query += `
GROUP BY ServiceName, SpanName
ORDER BY reqs DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("trace overview: %w", err)
	}
	defer rows.Close()

	var out []storage.OperationStats
	for rows.Next() {
		var (
			op    storage.OperationStats
			quant []float64
		)
		if err := rows.Scan(&op.Service, &op.Operation, &op.Count, &op.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning overview row: %w", err)
		}
		op.P50, op.P95, op.P99 = nsQuantiles(quant)
		out = append(out, op)
	}
	return out, rows.Err()
}

// nsQuantiles converts a quantiles() result (nanoseconds as float) into
// durations; missing entries stay zero.
func nsQuantiles(quant []float64) (p50, p95, p99 time.Duration) {
	get := func(i int) time.Duration {
		if i >= len(quant) {
			return 0
		}
		return time.Duration(quant[i])
	}
	return get(0), get(1), get(2)
}
