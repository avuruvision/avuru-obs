package clickhouse

import (
	"context"
	"fmt"
	"time"
)

// Per-project retention is enforced here rather than by a table TTL. The
// telemetry tables are shared across tenants and a ClickHouse TTL expression
// applies to a whole table, not to the rows of one `Tenant` — so a project
// that wants a shorter window than the install's global retention is trimmed
// by a scheduled hub job issuing bounded lightweight mutations
// (design/2026-07-27-projects-completion.md). The global TTL stays the coarse
// backstop, which is why only shorter-than-global windows are accepted.

// trimTable is one telemetry table the trimmer can scope by tenant, with the
// column that carries its event time. Every entry is a fact table whose rows
// are telemetry; the small state tables beside them (alert_state,
// error_issue_status, profiling_stacks) are deliberately absent for the same
// reason ApplyRetention leaves them alone — they are bounded, and dropping
// them would lose triage/evaluator state rather than old telemetry.
//
// otel_traces_trace_id_ts is absent too, but for a different reason: the
// exporter's companion index carries no Tenant column, so it cannot be scoped.
// It ages out on the global TTL like it always has.
type trimTable struct{ table, timeCol string }

var trimTables = []trimTable{
	{"otel_traces", "Timestamp"},
	{"otel_logs", "Timestamp"},
	{"otel_metrics_gauge", "TimeUnix"},
	{"otel_metrics_sum", "TimeUnix"},
	{"otel_metrics_histogram", "TimeUnix"},
	{"otel_metrics_exponential_histogram", "TimeUnix"},
	{"otel_metrics_summary", "TimeUnix"},
	{"profiling_samples", "Timestamp"},
	{"error_events", "Timestamp"},
	{"alert_history", "FiredAt"},
}

// TrimTenant deletes tenant rows older than cutoff and returns the tables it
// issued a mutation on (empty when there was nothing to do).
//
// Three guards keep a scheduled job from becoming a load problem, because a
// mutation is a part rewrite, not a cheap delete:
//
//   - a table that does not exist is skipped, so an install running without the
//     logs/profiling/alerting modules is not an error path;
//   - a table with an unfinished mutation is skipped, so a slow trim is never
//     re-issued on top of itself every tick;
//   - a table with no row older than cutoff is skipped, so the steady state
//     (yesterday's trim already done) costs one indexed lookup per table and
//     issues nothing.
//
// Mutations are fired asynchronously (ClickHouse's default): the caller is a
// background tick with nothing to wait for, and holding the connection open
// for a multi-minute part rewrite would only add a timeout to lose.
func (s *Store) TrimTenant(ctx context.Context, tenant string, cutoff time.Time) ([]string, error) {
	if tenant == "" {
		return nil, fmt.Errorf("trim tenant: empty tenant")
	}
	// existingTables (stats.go) is the same module-aware guard the storage
	// page uses: a table for an inactive module was never created.
	present, err := s.existingTables(ctx)
	if err != nil {
		return nil, err
	}
	busy, err := s.tablesWithRunningMutations(ctx)
	if err != nil {
		return nil, err
	}
	var trimmed []string
	for _, t := range trimTables {
		if !present[t.table] || busy[t.table] {
			continue
		}
		has, err := s.hasRowsBefore(ctx, t, tenant, cutoff)
		if err != nil {
			return trimmed, err
		}
		if !has {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE Tenant = ? AND %s < ?", s.db, t.table, t.timeCol)
		if err := s.conn.Exec(ctx, q, tenant, cutoff); err != nil {
			return trimmed, fmt.Errorf("trim %s for tenant %q: %w", t.table, tenant, err)
		}
		trimmed = append(trimmed, t.table)
	}
	return trimmed, nil
}

// hasRowsBefore reports whether the tenant has anything older than cutoff.
// LIMIT 1 on a Tenant-leading sort key: the answer comes from one granule, so
// the check costs far less than the mutation it avoids.
func (s *Store) hasRowsBefore(ctx context.Context, t trimTable, tenant string, cutoff time.Time) (bool, error) {
	q := fmt.Sprintf("SELECT 1 FROM %s.%s WHERE Tenant = ? AND %s < ? LIMIT 1", s.db, t.table, t.timeCol)
	rows, err := s.conn.Query(ctx, q, tenant, cutoff)
	if err != nil {
		return false, fmt.Errorf("check %s for tenant %q: %w", t.table, tenant, err)
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// tablesWithRunningMutations is the set of tables with a mutation still in
// flight. Skipping those is what bounds the job: the previous trim of a large
// table can outlive the interval, and queueing a second identical mutation
// behind it would multiply the work rather than repeat it.
func (s *Store) tablesWithRunningMutations(ctx context.Context) (map[string]bool, error) {
	rows, err := s.conn.Query(ctx,
		"SELECT DISTINCT table FROM system.mutations WHERE database = ? AND is_done = 0", s.db)
	if err != nil {
		return nil, fmt.Errorf("list running mutations: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan mutation table: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}
