package clickhouse

import (
	"context"
	"fmt"
	"strings"

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
WHERE Tenant = ?
  AND Timestamp >= ? AND Timestamp < ?`
	args := []any{q.Tenant, q.Range.Start, q.Range.End}

	if q.Service != "" {
		query += ` AND ServiceName = ?`
		args = append(args, q.Service)
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

// LogsForTrace returns all logs correlated to a trace, oldest-first (matches
// the waterfall reading order).
func (s *Store) LogsForTrace(ctx context.Context, tenant, traceID string) ([]storage.LogRecord, error) {
	const query = `
SELECT ` + logColumns + `
FROM otel_logs
WHERE Tenant = ? AND TraceId = ?
ORDER BY Timestamp ASC`
	rows, err := s.conn.Query(ctx, query, tenant, traceID)
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
