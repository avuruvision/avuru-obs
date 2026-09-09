package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// severityFloor maps a severity name to the lowest OTel SeverityNumber in its
// band, for `SeverityNumber >= floor` filtering (TRACE 1-4, DEBUG 5-8,
// INFO 9-12, WARN 13-16, ERROR 17-20, FATAL 21-24).
var severityFloor = map[string]uint8{
	"TRACE": 1, "DEBUG": 5, "INFO": 9,
	"WARN": 13, "WARNING": 13, "ERROR": 17, "FATAL": 21,
}

const logColumns = `Timestamp, SeverityText, ServiceName, Body, TraceId, SpanId, LogAttributes`

// SearchLogs returns log records newest-first with keyset pagination
// (Timestamp + TraceId + SpanId tiebreaker).
func (s *Store) SearchLogs(ctx context.Context, q storage.LogQuery) (storage.LogPage, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `
SELECT ` + logColumns + `
FROM otel_logs
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}

	if sql, sargs := logSourceFilter(q); sql != "" {
		query += sql
		args = append(args, sargs...)
	}
	if q.MinSeverity != "" {
		if floor, ok := severityFloor[strings.ToUpper(q.MinSeverity)]; ok {
			query += ` AND SeverityNumber >= ?`
			args = append(args, floor)
		}
	}
	if q.Query != "" {
		// Substring match (case-insensitive). The idx_lower_body text index
		// is token-based; substring search trades it for flexibility — fine
		// within the tenant+time partition at eval scale.
		query += ` AND positionCaseInsensitive(Body, ?) > 0`
		args = append(args, q.Query)
	}
	query, args = logTagFilters(query, q.Tags, args)
	if q.Cursor != nil {
		query += ` AND (Timestamp, TraceId, SpanId) < (?, ?, ?)`
		args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID, q.Cursor.SpanID)
	}
	query += `
ORDER BY Timestamp DESC, TraceId DESC, SpanId DESC
LIMIT ?`
	args = append(args, limit+1) // one extra row to detect the next page

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.LogPage{}, fmt.Errorf("searching logs: %w", err)
	}
	defer rows.Close()

	var page storage.LogPage
	for rows.Next() {
		var r storage.LogRecord
		if err := rows.Scan(&r.Timestamp, &r.Severity, &r.Service, &r.Body, &r.TraceID, &r.SpanID, &r.Attributes); err != nil {
			return storage.LogPage{}, fmt.Errorf("scanning log row: %w", err)
		}
		page.Logs = append(page.Logs, r)
	}
	if err := rows.Err(); err != nil {
		return storage.LogPage{}, err
	}

	if len(page.Logs) > limit {
		page.Logs = page.Logs[:limit]
		last := page.Logs[limit-1]
		page.NextCursor = &storage.LogCursor{Timestamp: last.Timestamp, TraceID: last.TraceID, SpanID: last.SpanID}
	}
	return page, nil
}

// logSourceFilter renders the service side of a log read: one OR branch per
// source when sources are set, each narrowed to its services and, per needle
// list, to the bodies containing one of them (multiSearchAny — exact, one
// pass); the plain equality when only Service is. The sort key leads with
// ServiceName after the time bucket, so each branch prunes to its own
// service's granules before the body is scanned.
func logSourceFilter(q storage.LogQuery) (string, []any) {
	if len(q.Sources) == 0 {
		if q.Service == "" {
			return "", nil
		}
		return ` AND ServiceName = ?`, []any{q.Service}
	}
	var branches []string
	var args []any
	for _, src := range q.Sources {
		branch := `ServiceName IN (?)`
		args = append(args, src.Services)
		for _, needles := range src.BodyAll {
			if len(needles) == 0 {
				continue
			}
			branch += ` AND multiSearchAny(Body, ?)`
			args = append(args, needles)
		}
		branches = append(branches, "("+branch+")")
	}
	return ` AND (` + strings.Join(branches, " OR ") + `)`, args
}

// logTagFilters is tagFilters for the log table, where a record's own
// attributes live in LogAttributes rather than SpanAttributes. Business tags
// (see TagPrefix) describe the emitting workload and so read
// ResourceAttributes, exactly as they do on traces — that shared rule is what
// makes one filter string mean the same thing on both screens.
func logTagFilters(query string, tags map[string]string, args []any) (string, []any) {
	for _, k := range sortedKeys(tags) {
		if isResourceTag(k) {
			query += ` AND ResourceAttributes[?] = ?`
		} else {
			query += ` AND LogAttributes[?] = ?`
		}
		args = append(args, k, tags[k])
	}
	return query, args
}

// LogsForTrace returns all logs correlated to a trace, oldest-first (matches
// the waterfall reading order).
func (s *Store) LogsForTrace(ctx context.Context, tenants []string, traceID string) ([]storage.LogRecord, error) {
	if err := requireTenants(tenants); err != nil {
		return nil, fmt.Errorf("logs for trace: %w", err)
	}
	const query = `
SELECT ` + logColumns + `
FROM otel_logs
WHERE Tenant IN (?) AND TraceId = ?
ORDER BY Timestamp ASC`
	rows, err := s.conn.Query(ctx, query, tenants, traceID)
	if err != nil {
		return nil, fmt.Errorf("logs for trace %s: %w", traceID, err)
	}
	defer rows.Close()

	var out []storage.LogRecord
	for rows.Next() {
		var r storage.LogRecord
		if err := rows.Scan(&r.Timestamp, &r.Severity, &r.Service, &r.Body, &r.TraceID, &r.SpanID, &r.Attributes); err != nil {
			return nil, fmt.Errorf("scanning log row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServicePresence probes the raw signal tables for service names, one query per
// requested signal.
//
// It exists to answer a question ListServices structurally cannot: ListServices
// counts entry spans, because that is the population RED is defined over, so a
// workload shipping logs and no server spans is absent from it. Treating that
// absence as "the service reported nothing" is a false statement about a live
// workload, and it is the one this method lets callers stop making.
//
// Spans counted here are of EVERY kind, unlike ListServices' Server/Consumer
// restriction: a job that only calls out has no entry span and is still a name
// someone can ask about. ExcludeAux is honoured for spans so the two
// populations cannot disagree about whether a health-check-only workload
// "reported". It cannot be honoured for logs — otel_logs has no SpanName or
// http.route to match on — so a log-only service is present here even if every
// line came from a health check.
func (s *Store) ServicePresence(ctx context.Context, q storage.ServiceQuery, signals []storage.Signal) ([]storage.ServicePresence, error) {
	if len(signals) == 0 {
		return nil, nil
	}
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	found := map[string]*storage.ServicePresence{}
	get := func(name string) *storage.ServicePresence {
		if p, ok := found[name]; ok {
			return p
		}
		p := &storage.ServicePresence{Name: name}
		found[name] = p
		return p
	}

	for _, sig := range signals {
		var (
			query string
			args  []any
			apply func(p *storage.ServicePresence, n, errs uint64)
		)
		switch sig {
		case storage.SignalLogs:
			// The countIf bind sits in the SELECT list, AHEAD of the WHERE
			// binds: arguments bind in STATEMENT order, not logical order.
			query = `
SELECT ServiceName, count() AS n, countIf(SeverityNumber >= ?) AS errs, max(Timestamp) AS newest
FROM otel_logs
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?
  AND ServiceName != ''
GROUP BY ServiceName`
			args = []any{severityFloor["ERROR"], tenants, q.Range.Start, q.Range.End}
			apply = func(p *storage.ServicePresence, n, errs uint64) {
				p.LogRecords, p.ErrorLogRecords = n, errs
			}
		case storage.SignalSpans:
			query = `
SELECT ServiceName, count() AS n, toUInt64(0) AS errs, max(Timestamp) AS newest
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?
  AND ServiceName != ''`
			args = []any{tenants, q.Range.Start, q.Range.End}
			if q.ExcludeAux {
				query += auxExclusion("")
			}
			query += `
GROUP BY ServiceName`
			apply = func(p *storage.ServicePresence, n, _ uint64) { p.Spans = n }
		default:
			return nil, fmt.Errorf("service presence: unknown signal %q", sig)
		}

		if err := func() error {
			rows, err := s.conn.Query(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("service presence (%s): %w", sig, err)
			}
			defer rows.Close()
			for rows.Next() {
				var (
					name    string
					n, errs uint64
					newest  time.Time
				)
				if err := rows.Scan(&name, &n, &errs, &newest); err != nil {
					return fmt.Errorf("scanning service presence (%s): %w", sig, err)
				}
				p := get(name)
				apply(p, n, errs)
				if newest.After(p.LastSeen) {
					p.LastSeen = newest
				}
			}
			return rows.Err()
		}(); err != nil {
			return nil, err
		}
	}

	// Sorted by name: the caller matches on it and, on a miss, ranks it for
	// "did you mean" — both want a stable order rather than a map's.
	out := make([]storage.ServicePresence, 0, len(found))
	for _, p := range found {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
