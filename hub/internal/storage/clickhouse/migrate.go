package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/migrations"
)

// schemaMigrationsTable is the ledger; its presence is also how SchemaStatus
// tells "never migrated" apart from "cannot read the database".
const schemaMigrationsTable = "schema_migrations"

// Migrate applies any unapplied embedded migrations for the active modules,
// recording each in the `<db>.schema_migrations` ledger. Idempotent:
// already-applied versions are skipped, so re-running is a no-op — enabling a
// module later is just a re-run with a bigger set. ClickHouse DDL is not
// transactional — every statement is `IF NOT EXISTS` and the ledger row is
// the commit marker.
//
// Safe to run CONCURRENTLY (several hub replicas, or a replica racing the
// chart's migrate Job) with no lock, and that is a property to preserve rather
// than a happy accident: every statement in every .sql is `IF NOT EXISTS`-
// guarded (enforced by TestEveryStatementIsIdempotent), the ledger is read into
// a set so duplicate rows are inert, and each caller walks Expected top-down so
// none can reach a derived view before its base table exists.
func (s *Store) Migrate(ctx context.Context, active modules.Set) error {
	if err := s.ensureLedger(ctx); err != nil {
		return err
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, version := range migrations.Expected(active) {
		if applied[version] {
			continue
		}
		body, err := migrations.FS.ReadFile(version)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", version, err)
		}
		for _, stmt := range migrations.Statements(string(body), s.db) {
			if err := s.conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("applying migration %s: %w", version, err)
			}
		}
		if err := s.conn.Exec(ctx,
			fmt.Sprintf("INSERT INTO %s.%s (version) VALUES (?)", s.db, schemaMigrationsTable), version,
		); err != nil {
			return fmt.Errorf("recording migration %s: %w", version, err)
		}
	}
	return nil
}

// SchemaStatus compares the ledger against what this module set expects.
//
// An ABSENT ledger is reported as "nothing applied", not as an error: that is
// exactly the state a skipped migrate Job leaves behind (every hub query then
// fails with ClickHouse code 60), and the caller must be able to act on it
// rather than mistake it for a backend fault.
func (s *Store) SchemaStatus(ctx context.Context, active modules.Set) (storage.SchemaStatus, error) {
	out := storage.SchemaStatus{Database: s.db, Expected: migrations.Expected(active)}

	tables, err := s.existingTables(ctx)
	if err != nil {
		return out, fmt.Errorf("reading schema state: %w", err)
	}
	applied := map[string]bool{}
	if tables[schemaMigrationsTable] {
		if applied, err = s.appliedVersions(ctx); err != nil {
			return out, err
		}
	}

	for _, version := range out.Expected {
		if applied[version] {
			out.Applied = append(out.Applied, version)
		} else {
			out.Missing = append(out.Missing, version)
		}
	}
	out.Ready = len(out.Missing) == 0
	return out, nil
}

// ensureLedger creates the database and the migration ledger. Split out of
// Migrate so both it and any future repair path share one definition.
func (s *Store) ensureLedger(ctx context.Context) error {
	if err := s.conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+s.db); err != nil {
		return fmt.Errorf("creating database %s: %w", s.db, err)
	}
	if err := s.conn.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s.%s (version String, applied_at DateTime DEFAULT now()) ENGINE = MergeTree ORDER BY version",
		s.db, schemaMigrationsTable,
	)); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
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
	ErrorsDays   int
	AlertsDays   int
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
	if r.ErrorsDays > 0 {
		// error_events only: error_issue_status is tiny (bounded by triaged
		// issues) and keeping resolved-issue history is the point.
		q := fmt.Sprintf("ALTER TABLE %s.error_events MODIFY TTL toDateTime(Timestamp) + toIntervalDay(%d)", s.db, r.ErrorsDays)
		if err := s.conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("retention on error_events: %w", err)
		}
	}
	if r.AlertsDays > 0 {
		// alert_history only: alert_state is tiny (bounded by rule×target) and
		// is the evaluator's live memory, so it is never TTL'd.
		q := fmt.Sprintf("ALTER TABLE %s.alert_history MODIFY TTL toDateTime(FiredAt) + toIntervalDay(%d)", s.db, r.AlertsDays)
		if err := s.conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("retention on alert_history: %w", err)
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf("SELECT version FROM %s.%s", s.db, schemaMigrationsTable))
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
