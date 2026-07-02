package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// SearchTraces returns root-span summaries, newest first, with keyset
// pagination (full-precision timestamp + TraceId tiebreaker).
func (s *Store) SearchTraces(ctx context.Context, q storage.TraceQuery) (storage.TracePage, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `
SELECT Timestamp, TraceId, ServiceName, SpanName, Duration, StatusCode
FROM otel_traces
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?
  AND ParentSpanId = ''`
	args := []any{q.Tenant, q.Range.Start, q.Range.End}

	if q.Service != "" {
		query += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	if q.Operation != "" {
		query += ` AND SpanName = ?`
		args = append(args, q.Operation)
	}
	switch q.Status {
	case "error":
		query += ` AND StatusCode = 'Error'`
	case "ok":
		query += ` AND StatusCode != 'Error'`
	}
	if q.MinDuration > 0 {
		query += ` AND Duration >= ?`
		args = append(args, uint64(q.MinDuration.Nanoseconds()))
	}
	if q.MaxDuration > 0 {
		query += ` AND Duration <= ?`
		args = append(args, uint64(q.MaxDuration.Nanoseconds()))
	}
	query, args = tagFilters(query, q.Tags, args)
	if q.ExcludeAux {
		query += auxExclusion("")
	}

	// Order and the keyset cursor must agree on the sort key: Duration for
	// "slowest", Timestamp otherwise. The unused cursor field is simply ignored.
	switch q.Order {
	case "oldest":
		if q.Cursor != nil {
			query += ` AND (Timestamp, TraceId) > (?, ?)`
			args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID)
		}
		query += `
ORDER BY Timestamp ASC, TraceId ASC
LIMIT ?`
	case "slowest":
		if q.Cursor != nil {
			query += ` AND (Duration, TraceId) < (?, ?)`
			args = append(args, uint64(q.Cursor.Duration.Nanoseconds()), q.Cursor.TraceID)
		}
		query += `
ORDER BY Duration DESC, TraceId DESC
LIMIT ?`
	default: // newest
		if q.Cursor != nil {
			query += ` AND (Timestamp, TraceId) < (?, ?)`
			args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID)
		}
		query += `
ORDER BY Timestamp DESC, TraceId DESC
LIMIT ?`
	}
	args = append(args, limit+1) // one extra row to detect the next page

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.TracePage{}, fmt.Errorf("searching traces: %w", err)
	}
	defer rows.Close()

	var page storage.TracePage
	for rows.Next() {
		var (
			t   storage.TraceSummary
			dur uint64
		)
		if err := rows.Scan(&t.StartTime, &t.TraceID, &t.RootService, &t.RootOperation, &dur, &t.StatusCode); err != nil {
			return storage.TracePage{}, fmt.Errorf("scanning trace row: %w", err)
		}
		t.Duration = time.Duration(dur)
		page.Traces = append(page.Traces, t)
	}
	if err := rows.Err(); err != nil {
		return storage.TracePage{}, err
	}

	if len(page.Traces) > limit {
		page.Traces = page.Traces[:limit]
		last := page.Traces[limit-1]
		page.NextCursor = &storage.TraceCursor{Timestamp: last.StartTime, Duration: last.Duration, TraceID: last.TraceID}
	}
	if err := s.fillTraceAggregates(ctx, q.Tenant, q.Range, page.Traces); err != nil {
		return storage.TracePage{}, err
	}
	return page, nil
}

// fillTraceAggregates sets SpanCount/ErrorCount on summaries with one grouped
// query over the page's trace ids.
func (s *Store) fillTraceAggregates(ctx context.Context, tenant string, r storage.TimeRange, traces []storage.TraceSummary) error {
	if len(traces) == 0 {
		return nil
	}
	ids := make([]string, len(traces))
	byID := make(map[string]*storage.TraceSummary, len(traces))
	for i := range traces {
		ids[i] = traces[i].TraceID
		byID[traces[i].TraceID] = &traces[i]
	}

	// Widen the window: child spans can start after (or end before) the root.
	const query = `
SELECT TraceId, count() AS spans, countIf(StatusCode = 'Error') AS errors
FROM otel_traces
WHERE Tenant = ? AND TraceId IN ? AND Timestamp >= ? - INTERVAL 1 HOUR AND Timestamp < ? + INTERVAL 1 HOUR
GROUP BY TraceId`
	rows, err := s.conn.Query(ctx, query, tenant, ids, r.Start, r.End)
	if err != nil {
		return fmt.Errorf("aggregating traces: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id            string
			spans, errcnt uint64
		)
		if err := rows.Scan(&id, &spans, &errcnt); err != nil {
			return fmt.Errorf("scanning trace aggregate: %w", err)
		}
		if t, ok := byID[id]; ok {
			t.SpanCount, t.ErrorCount = spans, errcnt
		}
	}
	return rows.Err()
}

// GetTrace fetches a full span tree via the trace-id timestamp lookup table.
func (s *Store) GetTrace(ctx context.Context, tenant, traceID string) (storage.Trace, error) {
	var start, end time.Time
	lookup := s.conn.QueryRow(ctx,
		`SELECT min(Start), max(End) FROM otel_traces_trace_id_ts WHERE TraceId = ?`, traceID)
	if err := lookup.Scan(&start, &end); err != nil || start.IsZero() {
		return storage.Trace{}, storage.ErrNotFound
	}

	const query = `
SELECT TraceId, SpanId, ParentSpanId, ServiceName, SpanName, SpanKind,
       Timestamp, Duration, StatusCode, StatusMessage,
       SpanAttributes, ResourceAttributes,
       Events.Timestamp, Events.Name, Events.Attributes
FROM otel_traces
WHERE Tenant = ? AND TraceId = ?
  AND Timestamp >= ? - INTERVAL 1 HOUR AND Timestamp < ? + INTERVAL 1 HOUR
ORDER BY Timestamp ASC`
	rows, err := s.conn.Query(ctx, query, tenant, traceID, start, end)
	if err != nil {
		return storage.Trace{}, fmt.Errorf("fetching trace %s: %w", traceID, err)
	}
	defer rows.Close()

	tr := storage.Trace{TraceID: traceID}
	for rows.Next() {
		var (
			sp      storage.Span
			dur     uint64
			evTimes []time.Time
			evNames []string
			evAttrs []map[string]string
		)
		if err := rows.Scan(&sp.TraceID, &sp.SpanID, &sp.ParentSpanID, &sp.Service, &sp.Operation, &sp.Kind,
			&sp.StartTime, &dur, &sp.StatusCode, &sp.StatusMessage,
			&sp.Attributes, &sp.ResourceAttributes,
			&evTimes, &evNames, &evAttrs); err != nil {
			return storage.Trace{}, fmt.Errorf("scanning span: %w", err)
		}
		sp.Duration = time.Duration(dur)
		for i := range evNames {
			ev := storage.SpanEvent{Name: evNames[i]}
			if i < len(evTimes) {
				ev.Time = evTimes[i]
			}
			if i < len(evAttrs) {
				ev.Attributes = evAttrs[i]
			}
			sp.Events = append(sp.Events, ev)
		}
		tr.Spans = append(tr.Spans, sp)
	}
	if err := rows.Err(); err != nil {
		return storage.Trace{}, err
	}
	if len(tr.Spans) == 0 {
		return storage.Trace{}, storage.ErrNotFound
	}
	return tr, nil
}
