package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// issueSortExpr maps a sort key to an ORDER BY over the grouped aliases.
var issueSortExpr = map[string]string{
	"":          "LastSeen DESC",
	"lastSeen":  "LastSeen DESC",
	"firstSeen": "FirstSeen DESC",
	"count":     "Cnt DESC",
}

// SearchErrorIssues returns fingerprint-grouped issues. The window (Range)
// selects which issues to show (those with an occurrence in it); the
// aggregates are all-time so FirstSeen/LastSeen and regression are correct
// regardless of the window. Triage state is LEFT JOINed from
// error_issue_status FINAL (bounded by triaged issues, so FINAL is cheap), and
// the effective status is derived in SQL so status filtering happens before
// LIMIT.
func (s *Store) SearchErrorIssues(ctx context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	order, ok := issueSortExpr[q.Sort]
	if !ok {
		return nil, fmt.Errorf("invalid sort %q", q.Sort)
	}

	// Inner: fingerprints active in the window (+ optional filters).
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	inner := `SELECT DISTINCT Fingerprint FROM error_events
WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenants, q.Range.Start, q.Range.End}
	if q.Service != "" {
		inner += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	if q.Query != "" {
		inner += ` AND positionCaseInsensitive(concat(ExceptionType, ' ', ExceptionMessage), ?) > 0`
		args = append(args, q.Query)
	}

	// Outer status filter over the derived effective status.
	statusFilter := ""
	switch q.Status {
	case "", "all":
		statusFilter = ""
	case "unresolved":
		statusFilter = `WHERE RawStatus = 'unresolved' OR (RawStatus = 'resolved' AND LastSeen > StatusAt)`
	case "resolved":
		statusFilter = `WHERE RawStatus = 'resolved' AND LastSeen <= StatusAt`
	case "ignored":
		statusFilter = `WHERE RawStatus = 'ignored'`
	default:
		return nil, fmt.Errorf("invalid status %q", q.Status)
	}

	query := `
SELECT Fingerprint, Service, Type, Message, Source, LastTraceID, FirstSeen, LastSeen, Cnt, RawStatus, StatusAt
FROM (
  SELECT
    e.Fingerprint                              AS Fingerprint,
    argMax(e.ServiceName, e.Timestamp)         AS Service,
    argMax(e.ExceptionType, e.Timestamp)       AS Type,
    argMax(e.ExceptionMessage, e.Timestamp)    AS Message,
    argMax(toString(e.Source), e.Timestamp)    AS Source,
    argMax(e.TraceId, e.Timestamp)             AS LastTraceID,
    min(e.Timestamp)                           AS FirstSeen,
    max(e.Timestamp)                           AS LastSeen,
    count()                                    AS Cnt,
    any(toString(s.Status))                    AS RawStatus,
    any(s.UpdatedAt)                           AS StatusAt
  FROM error_events e
  LEFT JOIN (SELECT Tenant, Fingerprint, Status, UpdatedAt FROM error_issue_status FINAL) s
    ON s.Tenant = e.Tenant AND s.Fingerprint = e.Fingerprint
  WHERE e.Tenant IN (?) AND e.Fingerprint IN (` + inner + `)
  GROUP BY e.Fingerprint
)
` + statusFilter + `
ORDER BY ` + order + `
LIMIT ?`

	// e.Tenant bind, then the inner subquery's binds, then limit. The outer
	// filter and the inner subquery bind the SAME resolved set — two separate
	// binds of one slice, the trap this site has always carried.
	full := append([]any{tenants}, args...)
	full = append(full, limit)

	rows, err := s.conn.Query(ctx, query, full...)
	if err != nil {
		return nil, fmt.Errorf("search error issues: %w", err)
	}
	defer rows.Close()

	var out []storage.ErrorIssue
	for rows.Next() {
		var (
			iss       storage.ErrorIssue
			rawStatus string
			statusAt  time.Time
		)
		if err := rows.Scan(&iss.Fingerprint, &iss.Service, &iss.Type, &iss.Message, &iss.Source,
			&iss.LastTraceID, &iss.FirstSeen, &iss.LastSeen, &iss.Count, &rawStatus, &statusAt); err != nil {
			return nil, fmt.Errorf("scanning error issue: %w", err)
		}
		iss.Status = rawStatus
		iss.Regressed = rawStatus == "resolved" && iss.LastSeen.After(statusAt)
		out = append(out, iss)
	}
	return out, rows.Err()
}

// GetErrorIssue returns one issue's all-time aggregate, or ErrNotFound.
func (s *Store) GetErrorIssue(ctx context.Context, tenants []string, fingerprint uint64) (storage.ErrorIssue, error) {
	if err := requireTenants(tenants); err != nil {
		return storage.ErrorIssue{}, fmt.Errorf("get error issue: %w", err)
	}
	const query = `
SELECT
  e.Fingerprint,
  argMax(e.ServiceName, e.Timestamp),
  argMax(e.ExceptionType, e.Timestamp),
  argMax(e.ExceptionMessage, e.Timestamp),
  argMax(toString(e.Source), e.Timestamp),
  argMax(e.TraceId, e.Timestamp),
  min(e.Timestamp), max(e.Timestamp), count(),
  any(toString(s.Status)), any(s.UpdatedAt)
FROM error_events e
LEFT JOIN (SELECT Tenant, Fingerprint, Status, UpdatedAt FROM error_issue_status FINAL) s
  ON s.Tenant = e.Tenant AND s.Fingerprint = e.Fingerprint
WHERE e.Tenant IN (?) AND e.Fingerprint = ?
GROUP BY e.Fingerprint`

	// The status join stays per-tenant; with a multi-tenant set, any() picks an
	// arbitrary member's triage when tenants disagree on the same fingerprint.
	var (
		iss       storage.ErrorIssue
		rawStatus string
		statusAt  time.Time
	)
	err := s.conn.QueryRow(ctx, query, tenants, fingerprint).Scan(
		&iss.Fingerprint, &iss.Service, &iss.Type, &iss.Message, &iss.Source,
		&iss.LastTraceID, &iss.FirstSeen, &iss.LastSeen, &iss.Count, &rawStatus, &statusAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrorIssue{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.ErrorIssue{}, fmt.Errorf("get error issue %d: %w", fingerprint, err)
	}
	iss.Status = rawStatus
	iss.Regressed = rawStatus == "resolved" && iss.LastSeen.After(statusAt)
	return iss, nil
}

// ListErrorEvents returns an issue's occurrences newest-first, keyset-paginated
// on (Timestamp, TraceId, SpanId).
func (s *Store) ListErrorEvents(ctx context.Context, q storage.ErrorEventQuery) (storage.ErrorEventPage, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := `
SELECT Timestamp, ServiceName, ExceptionType, ExceptionMessage, ExceptionStacktrace,
       TraceId, SpanId, toString(Source), Environment, SdkName, SdkVersion, Attributes
FROM error_events
WHERE Tenant IN (?) AND Fingerprint = ?`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Fingerprint}
	if !q.Range.Start.IsZero() {
		query += ` AND Timestamp >= ? AND Timestamp < ?`
		args = append(args, q.Range.Start, q.Range.End)
	}
	if q.Cursor != nil {
		query += ` AND (Timestamp, TraceId, SpanId) < (?, ?, ?)`
		args = append(args, q.Cursor.Timestamp, q.Cursor.TraceID, q.Cursor.SpanID)
	}
	query += `
ORDER BY Timestamp DESC, TraceId DESC, SpanId DESC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.ErrorEventPage{}, fmt.Errorf("list error events: %w", err)
	}
	defer rows.Close()

	var page storage.ErrorEventPage
	for rows.Next() {
		var e storage.ErrorEvent
		if err := rows.Scan(&e.Timestamp, &e.Service, &e.Type, &e.Message, &e.Stacktrace,
			&e.TraceID, &e.SpanID, &e.Source, &e.Environment, &e.SdkName, &e.SdkVersion, &e.Attributes); err != nil {
			return storage.ErrorEventPage{}, fmt.Errorf("scanning error event: %w", err)
		}
		page.Events = append(page.Events, e)
	}
	if err := rows.Err(); err != nil {
		return storage.ErrorEventPage{}, err
	}
	if len(page.Events) > limit {
		last := page.Events[limit-1]
		page.Events = page.Events[:limit]
		page.NextCursor = &storage.ErrorEventCursor{Timestamp: last.Timestamp, TraceID: last.TraceID, SpanID: last.SpanID}
	}
	return page, nil
}

// ErrorIssueHistogram buckets an issue's occurrences over the window.
func (s *Store) ErrorIssueHistogram(ctx context.Context, tenants []string, fingerprint uint64, r storage.TimeRange, points int) ([]storage.ErrorHistogramPoint, error) {
	if err := requireTenants(tenants); err != nil {
		return nil, fmt.Errorf("error histogram: %w", err)
	}
	if points <= 0 || points > 500 {
		points = 60
	}
	span := r.End.Sub(r.Start)
	if span <= 0 {
		return nil, fmt.Errorf("invalid time range")
	}
	bucket := int(span.Seconds()) / points
	if bucket < 1 {
		bucket = 1
	}
	const query = `
SELECT toStartOfInterval(Timestamp, INTERVAL ? second) AS bucket, count()
FROM error_events
WHERE Tenant IN (?) AND Fingerprint = ? AND Timestamp >= ? AND Timestamp < ?
GROUP BY bucket
ORDER BY bucket`
	rows, err := s.conn.Query(ctx, query, bucket, tenants, fingerprint, r.Start, r.End)
	if err != nil {
		return nil, fmt.Errorf("error histogram: %w", err)
	}
	defer rows.Close()

	var out []storage.ErrorHistogramPoint
	for rows.Next() {
		var p storage.ErrorHistogramPoint
		if err := rows.Scan(&p.Time, &p.Count); err != nil {
			return nil, fmt.Errorf("scanning histogram bucket: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var validIssueStatus = map[string]bool{"unresolved": true, "resolved": true, "ignored": true}

// SetErrorIssueStatus records a triage decision. ReplacingMergeTree keeps the
// newest row per (Tenant, Fingerprint) by the default UpdatedAt=now64(3), so a
// re-resolve after a regression just supersedes the old row.
func (s *Store) SetErrorIssueStatus(ctx context.Context, tenant string, fingerprint uint64, status string) error {
	if !validIssueStatus[status] {
		return fmt.Errorf("invalid status %q", status)
	}
	err := s.conn.Exec(ctx,
		"INSERT INTO error_issue_status (Tenant, Fingerprint, Status) VALUES (?, ?, ?)",
		tenant, fingerprint, status)
	if err != nil {
		return fmt.Errorf("set issue status: %w", err)
	}
	return nil
}
