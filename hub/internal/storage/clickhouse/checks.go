package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// RecordCheckResult appends one probe outcome. Append-only: this is a signal,
// not state, and a check that flapped is a fact worth keeping.
//
// Owned by the service-health module (migration 0020) — an install running
// without it has no table, and no scheduler to write to one.
func (s *Store) RecordCheckResult(ctx context.Context, r storage.CheckResult) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO endpoint_check_result
		(Tenant, CheckId, GroupName, Timestamp, Ok, Status, LatencyMs, Error, TraceId, SpanId)`)
	if err != nil {
		return fmt.Errorf("preparing check result: %w", err)
	}
	var ok uint8
	if r.OK {
		ok = 1
	}
	if err := batch.Append(
		r.Tenant, r.CheckID, r.Group, r.At, ok, uint16(r.Status),
		r.LatencyMs, r.Error, r.TraceID, r.SpanID,
	); err != nil {
		return fmt.Errorf("appending check result: %w", err)
	}
	return batch.Send()
}

// CheckResults returns the most recent results for one check, newest first.
func (s *Store) CheckResults(ctx context.Context, q storage.CheckQuery) ([]storage.CheckResult, error) {
	const query = `
SELECT CheckId, GroupName, Timestamp, Ok, Status, LatencyMs, Error, TraceId
FROM endpoint_check_result
WHERE Tenant IN (?) AND CheckId = ?
ORDER BY Timestamp DESC
LIMIT ?`
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.conn.Query(ctx, query, tenantsOrDefault(q.Tenants, q.Tenant), q.CheckID, limit)
	if err != nil {
		return nil, fmt.Errorf("check results: %w", err)
	}
	defer rows.Close()

	var out []storage.CheckResult
	for rows.Next() {
		var (
			r  storage.CheckResult
			ok uint8
			st uint16
		)
		if err := rows.Scan(&r.CheckID, &r.Group, &r.At, &ok, &st, &r.LatencyMs, &r.Error, &r.TraceID); err != nil {
			return nil, fmt.Errorf("scanning check result: %w", err)
		}
		r.OK, r.Status = ok == 1, int(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestCheckStates returns, per check, the most recent results within the
// window — newest first, capped per check.
//
// The cap is what the consecutive-failure rule needs and no more: the health
// evaluation asks "have the last N in a row failed?", so reading a bounded
// recent slice per check answers it without dragging the whole history back.
func (s *Store) LatestCheckStates(ctx context.Context, q storage.ServiceQuery, perCheck int) (map[string][]storage.CheckResult, error) {
	if perCheck <= 0 {
		perCheck = 3
	}
	// A window function ranks each check's rows by recency in one pass; the
	// alternative (a query per check) scales with the number of checks.
	const query = `
SELECT CheckId, GroupName, Timestamp, Ok, Status, LatencyMs, Error, TraceId
FROM (
    SELECT *, row_number() OVER (PARTITION BY CheckId ORDER BY Timestamp DESC) AS rn
    FROM endpoint_check_result
    WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ?
)
WHERE rn <= ?
ORDER BY CheckId, Timestamp DESC`

	rows, err := s.conn.Query(ctx, query, tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End, perCheck)
	if err != nil {
		return nil, fmt.Errorf("latest check states: %w", err)
	}
	defer rows.Close()

	out := map[string][]storage.CheckResult{}
	for rows.Next() {
		var (
			r  storage.CheckResult
			ok uint8
			st uint16
		)
		if err := rows.Scan(&r.CheckID, &r.Group, &r.At, &ok, &st, &r.LatencyMs, &r.Error, &r.TraceID); err != nil {
			return nil, fmt.Errorf("scanning check state: %w", err)
		}
		r.OK, r.Status = ok == 1, int(st)
		out[r.CheckID] = append(out[r.CheckID], r)
	}
	return out, rows.Err()
}
