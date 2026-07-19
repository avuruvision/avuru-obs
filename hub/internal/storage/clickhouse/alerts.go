package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// LoadAlertStates returns the latest state per (rule, target) for a tenant.
// FINAL collapses the ReplacingMergeTree to the newest row; the table is bounded
// by rule×target so FINAL is cheap (same reasoning as error_issue_status).
func (s *Store) LoadAlertStates(ctx context.Context, tenant string) ([]storage.AlertState, error) {
	rows, err := s.conn.Query(ctx, `
SELECT RuleName, Target, toString(Status), Since, LastNotifiedAt
FROM alert_state FINAL
WHERE Tenant = ? AND Status != 'ok'`, tenant)
	if err != nil {
		return nil, fmt.Errorf("load alert states: %w", err)
	}
	defer rows.Close()

	var out []storage.AlertState
	for rows.Next() {
		st := storage.AlertState{Tenant: tenant}
		if err := rows.Scan(&st.RuleName, &st.Target, &st.Status, &st.Since, &st.LastNotifiedAt); err != nil {
			return nil, fmt.Errorf("scan alert state: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SaveAlertStates upserts states by inserting new rows; ReplacingMergeTree keeps
// the newest (UpdatedAt defaults to now). Cleared targets (back to ok) are
// written as Status='ok' so a later FINAL reflects the resolution.
func (s *Store) SaveAlertStates(ctx context.Context, states []storage.AlertState) error {
	if len(states) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO alert_state (Tenant, RuleName, Target, Status, Since, LastNotifiedAt)")
	if err != nil {
		return fmt.Errorf("prepare alert_state batch: %w", err)
	}
	for _, st := range states {
		if err := batch.Append(st.Tenant, st.RuleName, st.Target, st.Status, st.Since, st.LastNotifiedAt); err != nil {
			return fmt.Errorf("append alert_state: %w", err)
		}
	}
	return batch.Send()
}

// AppendAlertHistory records fire/resolve events (append-only).
func (s *Store) AppendAlertHistory(ctx context.Context, entries []storage.AlertHistoryEntry) error {
	if len(entries) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx,
		"INSERT INTO alert_history (Tenant, RuleName, Target, Kind, Status, Reason, FiredAt)")
	if err != nil {
		return fmt.Errorf("prepare alert_history batch: %w", err)
	}
	for _, e := range entries {
		if err := batch.Append(e.Tenant, e.RuleName, e.Target, e.Kind, e.Status, e.Reason, e.FiredAt); err != nil {
			return fmt.Errorf("append alert_history: %w", err)
		}
	}
	return batch.Send()
}

// ListAlertHistory returns recent fire/resolve events, newest first.
func (s *Store) ListAlertHistory(ctx context.Context, q storage.AlertHistoryQuery) ([]storage.AlertHistoryEntry, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
SELECT RuleName, Target, toString(Kind), Status, Reason, FiredAt
FROM alert_history
WHERE Tenant = ?`
	args := []any{q.Tenant}
	if !q.Range.Start.IsZero() {
		query += ` AND FiredAt >= ? AND FiredAt < ?`
		args = append(args, q.Range.Start, q.Range.End)
	}
	query += `
ORDER BY FiredAt DESC
LIMIT ?`
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alert history: %w", err)
	}
	defer rows.Close()

	var out []storage.AlertHistoryEntry
	for rows.Next() {
		e := storage.AlertHistoryEntry{Tenant: q.Tenant}
		if err := rows.Scan(&e.RuleName, &e.Target, &e.Kind, &e.Status, &e.Reason, &e.FiredAt); err != nil {
			return nil, fmt.Errorf("scan alert history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
