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
// grouping by TraceId still lists the trace once, keyed on that child.
//
// Filter semantics: with a service filter, a trace matches when it CONTAINS a
// span of that service satisfying the operation/duration filters (status is
// judged on that service's spans) — so a gateway-rooted trace shows up when
// filtering by a downstream service. Without a service filter, the
// operation/status/duration filters match the representative, as do tags and
// aux exclusion always (a trace rooted at a health check stays hidden by
// default even when the filtered service participates in it).
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
WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ?
GROUP BY TraceId
HAVING 1 = 1`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}

	if q.Service != "" {
		// Participant match: one countIf so operation/duration must hold on
		// the SAME span of the filtered service — an Overview drill-down sends
		// a child service's entry-span operation, which never matches the
		// root's. Args append in statement order (positional placeholders).
		cond := `ServiceName = ?`
		args = append(args, q.Service)
		if q.Operation != "" {
			cond += ` AND SpanName = ?`
			args = append(args, q.Operation)
		}
		if q.MinDuration > 0 {
			cond += ` AND Duration >= ?`
			args = append(args, uint64(q.MinDuration.Nanoseconds()))
		}
		if q.MaxDuration > 0 {
			cond += ` AND Duration <= ?`
			args = append(args, uint64(q.MaxDuration.Nanoseconds()))
		}
		inner += ` AND countIf(` + cond + `) > 0`
		switch q.Status {
		case "error":
			inner += ` AND countIf(ServiceName = ? AND ` + errorSpanExpr("") + `) > 0`
			args = append(args, q.Service)
		case "ok":
			inner += ` AND countIf(ServiceName = ? AND ` + errorSpanExpr("") + `) = 0`
			args = append(args, q.Service)
		}
	} else {
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

// FindSpanTrace resolves the trace containing spanID. Deliberately no time
// bound: trace-id open (via the otel_traces_trace_id_ts lookup) is
// window-independent, and span search behaves the same; the idx_span_id
// bloom filter (0005) keeps it cheap on indexed parts.
func (s *Store) FindSpanTrace(ctx context.Context, tenants []string, spanID string) (string, error) {
	if err := requireTenants(tenants); err != nil {
		return "", fmt.Errorf("find span trace: %w", err)
	}
	row := s.conn.QueryRow(ctx,
		`SELECT TraceId FROM otel_traces WHERE Tenant IN (?) AND SpanId = ? LIMIT 1`, tenants, spanID)
	var traceID string
	if err := row.Scan(&traceID); err != nil || traceID == "" {
		return "", storage.ErrNotFound
	}
	return traceID, nil
}

// GetTrace fetches a full span tree via the trace-id timestamp lookup table.
func (s *Store) GetTrace(ctx context.Context, tenants []string, traceID string) (storage.Trace, error) {
	if err := requireTenants(tenants); err != nil {
		return storage.Trace{}, fmt.Errorf("get trace: %w", err)
	}
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
WHERE Tenant IN (?) AND TraceId = ?
  AND Timestamp >= ? - INTERVAL 1 HOUR AND Timestamp < ? + INTERVAL 1 HOUR
ORDER BY Timestamp ASC`
	rows, err := s.conn.Query(ctx, query, tenants, traceID, start, end)
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
