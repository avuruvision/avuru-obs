package clickhouse

import (
	"context"
	"fmt"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/storage/migrations"
)

// Migrate applies any unapplied embedded migrations, recording each in the
// `<db>.schema_migrations` ledger. Idempotent: already-applied versions are
// skipped, so re-running is a no-op. ClickHouse DDL is not transactional —
// every statement is `IF NOT EXISTS` and the ledger row is the commit marker.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+s.db); err != nil {
		return fmt.Errorf("creating database %s: %w", s.db, err)
	}
	if err := s.conn.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s.schema_migrations (version String, applied_at DateTime DEFAULT now()) ENGINE = MergeTree ORDER BY version",
		s.db,
	)); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, version := range migrations.Ordered {
		if applied[version] {
			continue
		}
		body, err := migrations.FS.ReadFile(version)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", version, err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if err := s.conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("applying migration %s: %w", version, err)
			}
		}
		if err := s.conn.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s.schema_migrations (version) VALUES (?)", s.db), version,
		); err != nil {
			return fmt.Errorf("recording migration %s: %w", version, err)
		}
	}
	return nil
}

// metricsTables are the five exporter metric-type tables (0003_metrics.sql).
var metricsTables = []string{
	"otel_metrics_gauge",
	"otel_metrics_sum",
	"otel_metrics_histogram",
	"otel_metrics_exponential_histogram",
	"otel_metrics_summary",
}

// Retention groups per-signal TTL day counts. A non-positive count is a
// no-op for that signal (the TTL is left unchanged).
type Retention struct {
	TracesDays   int
	LogsDays     int
	MetricsDays  int
	ProfilesDays int
}

// ApplyRetention sets per-signal TTL via `ALTER ... MODIFY TTL`. Retention
// lives outside the embedded `.sql` so it stays a per-deployment Helm value,
// not a schema edit.
func (s *Store) ApplyRetention(ctx context.Context, r Retention) error {
	if r.TracesDays > 0 {
		traceTables := []struct{ table, col string }{
			{"otel_traces", "Timestamp"},
			{"otel_traces_trace_id_ts", "Start"},
		}
		for _, t := range traceTables {
			q := fmt.Sprintf("ALTER TABLE %s.%s MODIFY TTL toDateTime(%s) + toIntervalDay(%d)", s.db, t.table, t.col, r.TracesDays)
			if err := s.conn.Exec(ctx, q); err != nil {
				return fmt.Errorf("retention on %s: %w", t.table, err)
			}
		}
	}
	if r.LogsDays > 0 {
		q := fmt.Sprintf("ALTER TABLE %s.otel_logs MODIFY TTL toDateTime(Timestamp) + toIntervalDay(%d)", s.db, r.LogsDays)
		if err := s.conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("retention on otel_logs: %w", err)
		}
	}
	if r.MetricsDays > 0 {
		for _, table := range metricsTables {
			q := fmt.Sprintf("ALTER TABLE %s.%s MODIFY TTL toDateTime(TimeUnix) + toIntervalDay(%d)", s.db, table, r.MetricsDays)
			if err := s.conn.Exec(ctx, q); err != nil {
				return fmt.Errorf("retention on %s: %w", table, err)
			}
		}
	}
	if r.ProfilesDays > 0 {
		// Samples only: profiling_stacks is small and self-deduplicating.
		q := fmt.Sprintf("ALTER TABLE %s.profiling_samples MODIFY TTL toDateTime(Timestamp) + toIntervalDay(%d)", s.db, r.ProfilesDays)
		if err := s.conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("retention on profiling_samples: %w", err)
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf("SELECT version FROM %s.schema_migrations", s.db))
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// splitStatements breaks a .sql file into individual statements on ';'.
// Line (`--`) comments are stripped FIRST so a ';' inside a comment can't
// split a statement (our DDL has no '--' or ';' inside string literals).
func splitStatements(sql string) []string {
	var stripped strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}

	var out []string
	for _, chunk := range strings.Split(stripped.String(), ";") {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(chunk))
	}
	return out
}
