package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// SearchTraces returns one summary per trace, newest first, with keyset
// pagination (full-precision timestamp + TraceId tiebreaker).
//
// A trace is represented by its EFFECTIVE ROOT — the parentless span if the
// store holds it, else the earliest span present (see repTuple). This surfaces
// orphaned/partial traces: when the true root was created upstream (an Istio/
// APISIX edge span, a browser span) and exported to a different backend, the
// app's child span here has a non-empty ParentSpanId whose parent is absent;
// grouping by TraceId still lists the trace once, keyed on that child. Filters
// (service/operation/status/duration/tags/aux) match the representative, so a
// complete trace behaves exactly as when we filtered on its root span.
func (s *Store) SearchTraces(ctx context.Context, q storage.TraceQuery) (storage.TracePage, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	// Inner: one row per trace. Every representative field uses the same
	// repTuple so they come from a single span (the effective root); count()/
	// countIf give trace-wide aggregates in the same pass (no second query).
	inner := `
SELECT
  TraceId,
  min(Timestamp)                                           AS StartTime,
  argMin(ServiceName, ` + repTuple + `)                    AS RootService,
  argMin(SpanName, ` + repTuple + `)                       AS RootOperation,
  argMin(Duration, ` + repTuple + `)                       AS RepDuration,
  argMin(StatusCode, ` + repTuple + `)                     AS RepStatusCode,
  argMin(SpanKind, ` + repTuple + `)                       AS RepKind,
  argMin(SpanAttributes['http.route'], ` + repTuple + `)   AS RepHttpRoute,
  argMin(SpanAttributes['db.system'], ` + repTuple + `)    AS RepDbSystem,
  argMin(SpanAttributes['db.operation'], ` + repTuple + `) AS RepDbOp,
  argMin(` + errorSpanExpr("") + `, ` + repTuple + `)      AS RepIsError,
  count()                                                  AS SpanCount,
  countIf(` + errorSpanExpr("") + `)                       AS ErrorCount
FROM otel_traces
WHERE Tenant = ? AND Timestamp >= ? AND Timestamp < ?
GROUP BY TraceId
HAVING 1 = 1`
	args := []any{q.Tenant, q.Range.Start, q.Range.End}

	if q.Service != "" {
		inner += ` AND RootService = ?`
		args = append(args, q.Service)
	}
	if q.Operation != "" {
		inner += ` AND RootOperation = ?`
		args = append(args, q.Operation)
	}
	switch q.Status {
	case "error":
		inner += ` AND RepIsError = 1`
	case "ok":
		inner += ` AND RepIsError = 0`
	}
	if q.MinDuration > 0 {
		inner += ` AND RepDuration >= ?`
		args = append(args, uint64(q.MinDuration.Nanoseconds()))
	}
	if q.MaxDuration > 0 {
		inner += ` AND RepDuration <= ?`
		args = append(args, uint64(q.MaxDuration.Nanoseconds()))
	}
	inner, args = tagFiltersRep(inner, q.Tags, args)
	if q.ExcludeAux {
		inner += auxExclusionRep()
	}

	// Outer: keyset cursor + order + limit over the grouped rows. The cursor
	// tuple compares aggregated columns (StartTime for newest/oldest,
	// RepDuration for slowest), so it must live here — post-aggregation — not
	// in the inner HAVING.
	query := `
SELECT TraceId, StartTime, RootService, RootOperation, RepDuration, RepStatusCode, SpanCount, ErrorCount
FROM (` + inner + `
)
WHERE 1 = 1`
	switch q.Order {
	case "oldest":
		if q.Cursor != nil {
			query += ` AND (StartTime, TraceId) > (?, ?)`
			args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID)
		}
		query += `
ORDER BY StartTime ASC, TraceId ASC
LIMIT ?`
	case "slowest":
		if q.Cursor != nil {
			query += ` AND (RepDuration, TraceId) < (?, ?)`
			args = append(args, uint64(q.Cursor.Duration.Nanoseconds()), q.Cursor.TraceID)
		}
		query += `
ORDER BY RepDuration DESC, TraceId DESC
LIMIT ?`
	default: // newest
		if q.Cursor != nil {
			query += ` AND (StartTime, TraceId) < (?, ?)`
			args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID)
		}
		query += `
ORDER BY StartTime DESC, TraceId DESC
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
		if err := rows.Scan(&t.TraceID, &t.StartTime, &t.RootService, &t.RootOperation, &dur, &t.StatusCode, &t.SpanCount, &t.ErrorCount); err != nil {
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
	return page, nil
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
       ScopeName, ScopeVersion,
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
			&sp.ScopeName, &sp.ScopeVersion,
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
