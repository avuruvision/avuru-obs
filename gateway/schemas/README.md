# ClickHouse schemas & migrations — MOVED

The telemetry-store DDL now lives in **`hub/internal/storage/migrations/`** and
is `go:embed`'d into the hub binary. Schema is applied by the hub-owned
migrator (`hub migrate`) — the single mechanism in compose AND k8s (M2).
See the M2 design spec: `docs/superpowers/specs/2026-06-15-m2-deployable-otlp-backend-design.md`.

| Migration | Contents |
|---|---|
| `0001_traces.sql` | `otel_traces` + `otel_traces_trace_id_ts` (+MV) |
| `0002_logs.sql` | `otel_logs` |
| `0003_metrics.sql` | the five `otel_metrics_*` tables (gauge/sum/histogram/exp_histogram/summary) |
| `0004` | **reserved**: profiles (M4, Coroot stack-dedup pattern) |
| `0005` | **reserved**: flows (v0.2, custom L4 tracer — was 0003 pre-release; safe to renumber, the ledger records filenames) |

## Rules (unchanged — now enforced by the hub migrator)

- **Append-only**: never edit an applied migration; fixes are new migrations.
  The `schema_migrations` ledger records applied versions; re-runs are no-ops.
- Retention/TTL is **not** in the `.sql` — it is applied env-driven by
  `hub migrate` (`AVURUOPS_RETENTION_TRACES_DAYS` / `AVURUOPS_RETENTION_LOGS_DAYS`),
  so per-deployment retention is a Helm value, not a schema edit.
- **The exporter owns the column contract**: `otel_traces`/`otel_logs` columns
  are frozen verbatim from the pinned `clickhouseexporter` (currently
  **0.154.0**, see `agent_docs/tech_stack.md`), captured via a
  `create_schema: true` run + `SHOW CREATE TABLE` on a scratch ClickHouse.
  Avuru-specific columns (e.g. `Tenant`) must use `DEFAULT`/`MATERIALIZED`
  so the exporter's explicit-column INSERT keeps working.
- **Collector upgrade = deliberate MR** that re-runs the contract-freeze
  procedure, diffs the generated DDL against these migrations, and adds a
  migration for any new columns. Integration tests (`hub` testcontainers) and
  e2e fail loudly on drift.
