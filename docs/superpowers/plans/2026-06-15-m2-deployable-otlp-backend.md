# M2 — Deployable OTLP Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (inline) — this plan is executed by its author in-session under a full-autonomy grant. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a vendor-neutral Helm chart that `helm install`s a Jaeger-replacement OTLP backend (gateway + single-node ClickHouse + hub) with hub-owned schema migrations, plus a trace-correlated logs view.

**Architecture:** Relocate the SQL migrations into the hub binary (`go:embed` + idempotent `schema_migrations` ledger, run via a new `hub migrate` subcommand and a Helm `pre-install,pre-upgrade` hook). Add a logs query path through the existing `storage.Store` seam and a `/logs` UI screen. Package everything as a hand-rolled chart (no operator).

**Tech Stack:** Go 1.26 (hub, `clickhouse-go/v2`, `embed`), Helm 3, ClickHouse 26.3 StatefulSet, Next.js static export + TanStack Query (UI), kind (e2e).

**Spec:** `docs/superpowers/specs/2026-06-15-m2-deployable-otlp-backend-design.md`

---

## File structure (decomposition)

| File | Responsibility |
|---|---|
| `hub/internal/storage/migrations/*.sql` | Embedded versioned DDL (moved from `gateway/schemas/`) |
| `hub/internal/storage/migrations/migrations.go` | `embed.FS` + ordered migration list |
| `hub/internal/storage/clickhouse/migrate.go` | `Migrate()` + `ApplyRetention()` against ClickHouse |
| `hub/cmd/hub/main.go` | `migrate` subcommand dispatch |
| `hub/internal/storage/store.go` | + `SearchLogs`, `LogsForTrace` types & interface methods |
| `hub/internal/storage/clickhouse/logs.go` | logs SQL |
| `hub/internal/storage/storagetest/fake.go` | + logs fakes |
| `hub/internal/api/logs.go` | logs handlers + DTOs |
| `hub/internal/api/router.go` | + 2 logs routes |
| `ui/src/lib/api-types.ts`, `hooks/use-logs-data.ts` | logs DTO mirror + query hooks |
| `ui/src/components/logs/*` | `/logs` screen |
| `ui/app/logs/page.tsx` | replace ComingSoon with real screen |
| `deploy/helm/avuruops/**` | the chart |
| `Makefile`, `.gitlab-ci.yml` | `e2e-helm` target + CI job |

---

## Phase 0 — Housekeeping

### Task 0.1: Clarify branch convention in AGENTS.md
**Files:** Modify `AGENTS.md` (Branch & push hygiene section)

- [ ] Reword rule 1 to distinguish milestone branches (`feature/<milestone>`) from ad-hoc agent tasks (`ai/<topic>`), both from `develop`, both `push -u` to their own upstream.
- [ ] Commit: `docs: clarify feature/ (milestone) vs ai/ (task) branch prefixes`

---

## Phase 1 — Hub-owned migrations

### Task 1.1: Relocate SQL + embed
**Files:** Create `hub/internal/storage/migrations/{0001_traces.sql,0002_logs.sql,migrations.go}`; delete `gateway/schemas/*.sql` (keep README pointing to new home).

- [ ] Move `gateway/schemas/0001_traces.sql` and `0002_logs.sql` verbatim into `hub/internal/storage/migrations/`.
- [ ] Strip the hardcoded `TTL ...` clause out of each `.sql` (retention becomes an `ALTER` driven by env — Task 1.4). Leave a comment: `-- TTL applied by hub migrate ApplyRetention (env-driven).`
- [ ] Write `migrations.go`:

```go
// Package migrations holds the hub-owned ClickHouse DDL, embedded into the
// binary so `hub migrate` is the single schema mechanism in compose AND k8s.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

// Ordered is the apply order; filenames are the version ids (lexical).
var Ordered = []string{"0001_traces.sql", "0002_logs.sql"}
```

- [ ] Verify: `cd hub && go build ./...`
- [ ] Commit: `refactor(hub): relocate ClickHouse DDL into embedded migrations`

### Task 1.2: Migrator with ledger (integration test first)
**Files:** Create `hub/internal/storage/clickhouse/migrate.go`; test in `integration_test.go`.

- [ ] Write integration test `TestMigrateIsIdempotent` (testcontainers, mirrors existing integration_test.go setup): run `Migrate` twice against a fresh CH; assert (a) `otel.otel_traces` and `otel.otel_logs` exist, (b) `otel.schema_migrations` has 2 rows, (c) second run is a no-op (still 2 rows, no error).
- [ ] Run: `cd hub && go test -tags=integration ./internal/storage/clickhouse/ -run TestMigrate` → FAIL (Migrate undefined).
- [ ] Implement `Migrate(ctx)`:
  - Ensure `CREATE DATABASE IF NOT EXISTS otel` then `CREATE TABLE IF NOT EXISTS otel.schema_migrations (version String, applied_at DateTime DEFAULT now()) ENGINE = MergeTree ORDER BY version`.
  - `SELECT version FROM otel.schema_migrations` → set of applied.
  - For each `migrations.Ordered` not applied: split the `.sql` on `;` into statements, `conn.Exec` each, then `INSERT` the version row.
  - Statements run outside a txn (ClickHouse DDL is not transactional) — the ledger row is the commit marker; idempotent `IF NOT EXISTS` DDL makes re-runs safe.
- [ ] Run the test → PASS.
- [ ] Commit: `feat(hub): idempotent ClickHouse migrator with schema_migrations ledger`

### Task 1.3: `hub migrate` subcommand
**Files:** Modify `hub/cmd/hub/main.go`.

- [ ] Before `flag.Parse()`, dispatch on `os.Args[1] == "migrate"`: build `ch.Config` from the same env vars `run()` uses, call `ch.New` (blocking, with a short retry loop ~60s for CH coming up), then `store.Migrate(ctx)` and `store.ApplyRetention(ctx, ...)`; exit 0/1. Keep `-healthcheck` working.
- [ ] Manual verify against compose CH: `AVURUOPS_CLICKHOUSE_ADDR=localhost:9000 go run ./cmd/hub migrate` prints applied versions, exits 0; second run is a no-op.
- [ ] Commit: `feat(hub): add 'hub migrate' subcommand`

### Task 1.4: Env-driven retention
**Files:** `hub/internal/storage/clickhouse/migrate.go`, store interface note.

- [ ] Add `ApplyRetention(ctx, tracesDays, logsDays int)`: `ALTER TABLE otel.otel_traces MODIFY TTL toDateTime(Timestamp) + toIntervalDay(N)` and the logs equivalent (use the real timestamp columns per each schema). No-op when days ≤ 0.
- [ ] `migrate` subcommand reads `AVURUOPS_RETENTION_TRACES_DAYS` (default 7), `AVURUOPS_RETENTION_LOGS_DAYS` (default 3).
- [ ] Extend `TestMigrate...` to assert TTL is set (query `system.tables`/`SHOW CREATE`).
- [ ] Commit: `feat(hub): env-driven per-signal retention (TTL) in migrate`

### Task 1.5: Wire compose to `hub migrate`
**Files:** `deploy/compose/docker-compose.yaml`, `deploy/compose/README.md`, `Makefile` (e2e/e2e-ui add a migrate step), `gateway/schemas/README.md`.

- [ ] Remove the `/docker-entrypoint-initdb.d` volume mount; add a one-shot `migrate` step (run `hub migrate` after CH healthy, before seeding) in `make e2e`/`e2e-ui`/`dev` flows.
- [ ] Verify: `make e2e` still green end-to-end.
- [ ] Commit: `refactor(compose): apply schema via 'hub migrate' (drop initdb.d)`

---

## Phase 2 — Correlated logs API

### Task 2.1: Store types + interface
**Files:** Modify `hub/internal/storage/store.go`.

- [ ] Add:

```go
type LogQuery struct {
	Tenant   string
	Range    TimeRange
	Service  string
	Severity string // "", or min SeverityText e.g. "ERROR"
	Query    string // full-text on Body
	Limit    int
	Cursor   *LogCursor
}
type LogCursor struct { Timestamp time.Time; ID string } // (Timestamp,TraceId+SpanId) tiebreak
type LogRecord struct {
	Timestamp time.Time
	Severity  string
	Service   string
	Body      string
	TraceID   string
	SpanID    string
	Attributes map[string]string
}
type LogPage struct { Logs []LogRecord; NextCursor *LogCursor }
```

- [ ] Add to `Store` interface: `SearchLogs(ctx, LogQuery) (LogPage, error)` and `LogsForTrace(ctx, tenant, traceID string) ([]LogRecord, error)`.
- [ ] Update `storagetest.Fake` with `Logs []LogRecord`, `LogPage`, `LastLogQuery` fields + the two methods (mirror the trace fakes). Build must stay green (`Store` is satisfied).
- [ ] Commit: `feat(hub): logs query types on the storage seam`

### Task 2.2: ClickHouse logs impl (integration test first)
**Files:** Create `hub/internal/storage/clickhouse/logs.go`; integration test.

- [ ] Integration test: insert 3 log rows (2 with a shared TraceId, varied SeverityText/Body), assert `SearchLogs` filters by service/severity/full-text and paginates by keyset; assert `LogsForTrace` returns the 2 correlated rows ordered by Timestamp.
- [ ] Implement queries against `otel.otel_logs` (columns confirmed: `Timestamp,SeverityText,SeverityNumber,ServiceName,Body,TraceId,SpanId,LogAttributes,Tenant`). Severity filter via `SeverityNumber >=` mapping; full-text via `lower(Body) LIKE` (the `idx_lower_body` text index backs it). Keyset on `(Timestamp DESC, TraceId, SpanId)`.
- [ ] Test → PASS. Commit: `feat(hub): ClickHouse logs search + trace correlation`

### Task 2.3: Logs HTTP API
**Files:** Create `hub/internal/api/logs.go`; modify `router.go`; test in `router_test.go`.

- [ ] Handler test (fake store): `GET /api/v1/logs?service=x&q=foo&severity=ERROR` returns the fake page + nextCursor; `GET /api/v1/traces/{id}/logs` returns the fake list; assert `LastLogQuery` parsed correctly.
- [ ] Implement `handleSearchLogs` + `handleLogsForTrace` mirroring `traces.go` (reuse `parseTimeRange`, `parseInt`, `tenant`, `writeJSON`, `badRequest`; add `encodeLogCursor`/`parseLogCursor`). DTOs `logRecordDTO`, `logsResponse`.
- [ ] Register: `GET /api/v1/logs`, `GET /api/v1/traces/{traceId}/logs`.
- [ ] Run: `cd hub && go test -race ./...` → PASS. Commit: `feat(hub): logs API (/api/v1/logs, /traces/{id}/logs)`

---

## Phase 3 — Logs UI

### Task 3.1: API types + hooks
**Files:** `ui/src/lib/api-types.ts` (+ `LogRecord`, `LogsResponse`), `ui/src/lib/query-keys.ts` (+ logs keys), `ui/src/hooks/use-logs-data.ts` (`useLogSearch` infinite query + `useTraceLogs`).

- [ ] Mirror the DTOs; `useLogSearch(time, filters)` (infinite, keyset) + `useTraceLogs(traceId)`.
- [ ] Verify: `cd ui && npx tsc --noEmit`. Commit: `feat(ui): logs data hooks`

### Task 3.2: `/logs` screen
**Files:** Create `ui/src/components/logs/{logs-screen.tsx,log-table.tsx,severity-badge.tsx}`; replace `ui/app/logs/page.tsx` ComingSoon with the real screen (Topbar + Suspense + LogsScreen).

- [ ] Dense table (time · severity badge · service · body · traceId link), severity/service/full-text filters in the URL (reuse `useURLState`, `useTimeRange`), infinite "Load more". A row's `traceId` links to `/traces?trace=<id>&tab=traces` (log→trace correlation — committed).
- [ ] Verify: `npm run lint && npm run build`. Commit: `feat(ui): /logs screen with search + log→trace correlation`

### Task 3.3 (stretch): span→logs panel
- [ ] If time allows: a "Logs" affordance in `span-detail.tsx` calling `useTraceLogs` filtered to the span. Marked stretch in the spec — skip without guilt if Phase 4/5 need the time.

---

## Phase 4 — Helm chart

### Task 4.1: Chart skeleton
**Files:** `deploy/helm/avuruops/{Chart.yaml,values.yaml,values.schema.json,templates/_helpers.tpl,.helmignore}`.

- [ ] `Chart.yaml` (apiVersion v2, appVersion pinned), `values.yaml` matching the spec's surface (image, gateway, ingress, clickhouse.external/persistence/resources, retention, auth), `values.schema.json` validating it, `_helpers.tpl` (fullname, labels, selectorLabels).
- [ ] Verify: `helm lint deploy/helm/avuruops`. Commit: `feat(helm): chart skeleton + values contract`

### Task 4.2: ClickHouse StatefulSet
**Files:** `templates/clickhouse-{config,statefulset,svc}.yaml`.

- [ ] ConfigMap (2 GB-tuned settings from compose), headless Service, StatefulSet (`replicas: 1`, `volumeClaimTemplates` with `storageClassName`/`size`, resources, image), all gated `{{- if not .Values.clickhouse.external.enabled }}`.
- [ ] Verify: `helm template ... | kubectl apply --dry-run=client -f -` (or `--validate=false` offline). Commit: `feat(helm): single-node ClickHouse StatefulSet + PVC + external toggle`

### Task 4.3: Gateway + hub + ingress + migrate hook
**Files:** `templates/{gateway-config,gateway-deploy,gateway-svc,hub-deploy,hub-svc,ingress,migrate-job}.yaml`.

- [ ] Gateway Deployment+Service (OTLP 4317/4318) + ConfigMap (the compose collector config, CH addr templated). Hub Deployment+Service+Ingress (env wires CH addr/retention; UI embedded). `migrate-job.yaml` = Helm hook `pre-install,pre-upgrade`, `hook-delete-policy: before-hook-creation,hook-succeeded`, runs `hub migrate`.
- [ ] Verify: `helm template` renders all; `helm lint` clean. Commit: `feat(helm): gateway, hub, ingress, migration hook`

---

## Phase 5 — Verification + CI

### Task 5.1: `make e2e-helm`
**Files:** `Makefile`, `deploy/helm/README.md`.

- [ ] Target: create kind cluster (on Colima) → build+load hub/gateway images → `helm install avuruops` (reduced CH memory for CI) → wait rollout → drive HotROD/seed via the gateway Service (port-forward) → reuse the `e2e/` Go assertions for traces **+** logs against the hub Service → `kind delete`.
- [ ] Run locally once; capture/trim CH memory if the Colima node is tight (spec risk #1).
- [ ] Commit: `test(e2e): make e2e-helm — kind install smoke (traces + logs)`

### Task 5.2: CI job + docs
**Files:** `.gitlab-ci.yml`, `agent_docs/testing.md`.

- [ ] Add `helm-lint` (no cluster: `helm lint` + `helm template | kubeconform`) path-filtered on `deploy/helm/**`. Document `e2e-helm` as a kind-runner TODO (same glibc/runner caveat class as e2e-ui).
- [ ] Commit: `ci: helm lint/template validation job`

### Task 5.3: Final gate
- [ ] `make check` green; `make e2e` green; `make e2e-helm` green; push branch.
- [ ] Summarize for the user; the PR `feature/m2-deployable-otlp-backend → develop` is the integration step (do NOT push to develop directly — AGENTS.md).

---

## Self-review notes
- **Spec coverage:** chart topology (P4), CH StatefulSet+persistence+external (4.2), hub-owned migrations+hook (P1), correlated logs API+UI (P2/P3), retention defaults (1.4), auth no-op seam (deferred — UI ships open; `auth.enabled` value exists, no provider code needed for no-op), verification (P5). The `auth.Provider` Go interface is NOT built in M2 (no-op = absence); `values.auth.enabled` is a forward placeholder only — noted so the downstream overlay has the knob.
- **Retention mechanism:** TTL removed from static `.sql`, applied by env-driven `ApplyRetention` — keeps embedded DDL static and values→env→ALTER clean.
- **Risk:** kind-on-Colima memory (spec risk #1) handled in 5.1 by a reduced CH profile.
