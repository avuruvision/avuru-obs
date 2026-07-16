# AEP: Error tracking — a derived signal plus a Sentry-protocol ingest

- **Date:** 2026-07-16
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Add an **error-tracking** module: the exceptions already flowing through
avuru-obs (span `exception` events, error spans, ERROR/FATAL logs) become
first-class **issues** — deduplicated by fingerprint, each with a stack trace,
an occurrence timeline, links to the originating trace, and a triage lifecycle
(unresolved / resolved / ignored, with regression detection when a resolved
issue recurs). Two ingestion paths, neither needing an app change: derivation
**in ClickHouse** from data you already send, and a **Sentry-protocol receiver
in the gateway** so existing Sentry SDKs — browser JS especially — point at
avuru-obs by changing a DSN. Ships as the first module born on the
[module framework](2026-07-15-module-framework.md).

## Motivation

Errors are detected today but only as counts (RED error rate, `errorCount` per
trace/service). The exception itself — type, message, stack, how often, since
when, which trace — is buried in span events and logs. An operator can see
"5% errors" but not "this NullPointerException, 4 000 times, since the 14:02
deploy, resolved last week and now back."

Two audiences feel it:

- **Backend/SRE** want zero-instrumentation error grouping. Their exceptions
  already reach us via OBI and OTLP; deriving issues in-database gives them a
  Sentry-class triage view with no SDK to deploy.
- **Frontend** need browser error capture — the one signal eBPF cannot reach.
  The Sentry SDK ecosystem is the de-facto standard, so accepting its wire
  protocol means adoption at the cost of a DSN change, not a rewrite.

Ties to the wedge: no app changes, OTel-semconv throughout, hub stays out of
the byte-path. Ties to [locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
derivation is ClickHouse materialized views at insert time; the Sentry ingest
is a gateway OCB receiver emitting standard OTel log records.

### Goals

- Issues grouped by a stable fingerprint (service + exception type + top
  normalized stack frames, message fallback), deduplicating across time.
- Issue list + detail: stack trace, occurrence histogram, first/last seen,
  count, source, and a link to a representative trace.
- Triage lifecycle: unresolved / resolved / ignored, plus **regression** — a
  resolved issue with a later occurrence is flagged, computed at read time.
- A Sentry-protocol ingest endpoint in the gateway (envelope + legacy store
  API) translating events to OTel log records, so any Sentry SDK works.
- Gated by the module framework: `modules.errorTracking.enabled` (default
  **true** — derivation is free value from existing data). The Sentry ingest
  **port** is a sub-flag, default **off** (it opens a network surface).

### Non-goals (v1)

- Alerting on new/spiking issues — depends on a notification subsystem
  avuru-obs doesn't have yet.
- Release tracking and source-map upload (deminified JS frames).
- Assignment / comment threads and affected-user counts.
- Session replay, uptime, cron monitoring.

## Solution

### Data model (`0006_errors.sql`, module `error-tracking`)

- `otel.error_events` — one row per occurrence: `Timestamp, Tenant,
  ServiceName, Fingerprint UInt64, Source Enum8('span','log','sentry'),
  ExceptionType, ExceptionMessage, ExceptionStacktrace, TraceId, SpanId,
  Environment, SdkName, SdkVersion, Attributes Map`. `MergeTree`,
  `ORDER BY (Tenant, Fingerprint, Timestamp)`.
- `otel.error_issue_status` — triage state: `Tenant, Fingerprint,
  Status Enum8, UpdatedAt`. `ReplacingMergeTree(UpdatedAt)` keyed
  `(Tenant, Fingerprint)` — last write wins (precedent: `profiling_stacks`).
- Three **materialized views** populate `error_events` at insert time, one per
  source: span `exception` events (`ARRAY JOIN Events.*`), error spans without
  an exception event (SQL copy of `errorSpanExpr()` — three-way KEEP-IN-SYNC
  with `status.go` and `span-status.ts`), and ERROR/FATAL logs
  (`SeverityNumber >= 17`; Sentry-origin rows self-tag via `avuru.error.source`).
  Each MV recomputes `Tenant` explicitly (the `DEFAULT` doesn't propagate into
  an MV) and fingerprints in SQL.

### Ingestion — Sentry receiver (gateway OCB module)

`gateway/sentryreceiver/`, a custom collector receiver: accepts
`POST /api/{project_id}/envelope/` (+ legacy `/store/`), auth via
`X-Sentry-Auth` or `?sentry_key=`, parses the envelope, and translates `event`
items to `plog.Logs` with `exception.*` semconv, `avuru.error.source=sentry`,
trace context, and `service.name` from project config. Unknown item types are
discarded, never 4xx'd (SDKs retry). The record flows through the existing
pipeline; the logs MV turns it into an issue, and it also appears in the Logs
explorer for free. `avuru.tenant` is stamped by the existing resourceprocessor.

### Read + triage API (hub)

`GET /api/v1/errors/issues` (status/service/q/sort filters),
`/issues/{fingerprint}`, `/issues/{fingerprint}/events` (keyset paginated),
`/issues/{fingerprint}/histogram`, and `POST /issues/{fingerprint}/status`.
Regression is read-time: `resolved && LastSeen > status.UpdatedAt`. Triage
writes are control-plane (precedent: profiles ingest). Routes registered only
when the module is active; tenant-scoped like every endpoint.

### UI

`/errors` page: issue list (status tabs, service filter, sort), detail panel
(stack trace, occurrence histogram, triage buttons, trace links), gated by the
module framework's `ModuleGate`.

### Upholds the enterprise seam

`Tenant` leads every new sort key; retention is `AVURUOPS_RETENTION_ERRORS_DAYS`
(default 30); storage stays behind `storage.Store`. Per-project DSN keys are a
later item, coupled to v0.2 auth.

### Alternatives considered

- **Hub-side derivation job** — a scheduler that lags ingest; MVs derive at
  insert with no moving parts.
- **Sentry endpoint in the hub** — puts the hub in the byte-path (rejected
  decision); a gateway receiver keeps it out.
- **Triage state outside ClickHouse** — needs a cross-store join for
  regression; `ReplacingMergeTree` co-locates it with the events.
- **Write-time regression flag** — read-time `LastSeen > resolved_at` is
  stateless and can't drift.

### Documented v1 limitations

- **Double counting**: one logical error that is both logged and recorded as a
  span event yields two issues (different sources/fingerprints). Read-time
  trace-correlation dedup is future work.
- **No backfill**: MVs derive only inserts after the migration.
- Retention differences (errors 30d vs traces/logs shorter) can leave a trace
  link dangling — the UI renders a "trace expired" state.

## Verification

- Hub integration tests: synthetic rows in `otel_traces`/`otel_logs` produce
  the right `error_events`; fingerprint stable across line-number changes;
  tenant derivation; triage → regression → re-resolve.
- Receiver unit tests: captured envelope fixtures (browser + one server SDK).
- e2e: POST an envelope to `:4319`, poll `/api/v1/errors/issues` until the
  `source: sentry` issue appears; assert derived issues from the seed and the
  triage POST. Playwright: issue list, detail, triage, trace link.
- Module gating: `modules.errorTracking.enabled=false` → no tables, `/errors`
  API 404, no sidebar entry.

## Roadmap

- [ ] AEP accepted
- [ ] `0006_errors.sql` + MVs + retention + `error-tracking` in the module registry
- [ ] Hub read API + DTOs
- [ ] Triage write + regression
- [ ] UI errors page + detail + triage
- [ ] Gateway `sentryreceiver` + OCB/compose/Helm wiring + e2e
- [ ] Docs (signal page, Sentry-SDK integration, API reference) + Compare pages
