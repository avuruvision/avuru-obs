# AEP: Projects completion — CRUD, per-project retention, status, chart toggles

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Finish the multi-project story v0.1 started (config-defined projects +
auto-discovery from data) and auth turns into a real boundary. This adds:
UI-authored **project CRUD** (config-defined projects stay read-only),
**per-project retention** (per-tenant ClickHouse TTL), **per-project system
status**, and **chart component toggles** so a secondary cluster can install
`gateway(+sensor)`-only against a shared ClickHouse. It depends on the auth
AEP's per-project grants and ingest keys — projects become an ownership +
retention + billing-of-storage unit, not just a query filter.

## Motivation

v0.1 projects are a UX filter: a `?project=` link and a sidebar switcher over
tenants discovered in data or declared in chart values. Once
[auth](2026-07-21-auth-oidc-rbac.md) makes a project a grant boundary and ingest
keys stamp a project at the write side, operators need to *manage* projects
(create/rename/archive without editing values and redeploying), keep different
projects for different windows (a noisy staging project should not hold 30 days
of traces just because prod does), and see health per project. This is the
roadmap's queued Projects work; it ties to the
[enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass)
(the `tenant` column and retention-policy objects were built in from v0.1
precisely so this lands without a rewrite).

### Goals

- **Project CRUD** in the hub + UI (Admin): create, rename/describe, archive.
  A `projects` table in ClickHouse; chart-declared projects merge in as
  read-only (the config+UI hybrid used for alert channels).
- **Per-project retention**: a retention-days setting per project applied as a
  ClickHouse TTL scoped by the `Tenant` column, overriding the global default.
- **Per-project system status**: the existing `/system/status` reported per
  selected project (retention, ingest rate, storage estimate, signal presence).
- **Chart component toggles**: values to install a subset (gateway-only, or
  gateway+sensor-only) that points at an external/shared ClickHouse — a
  secondary cluster ships telemetry to a central instance.
- **Auto-discovery preserved**: a project seen in data but not declared still
  appears (read-only) so nothing silently disappears.

### Non-goals

- **Per-user project preferences, favorites, dashboards** — later.
- **Cross-project queries / aggregation views** — projects stay isolated.
- **Billing / quota enforcement** — storage estimate is informational in v0.2.
- **Moving existing data between projects** — retag-in-place is out of scope.

## Solution

**Storage.** A `projects` table (ReplacingMergeTree + FINAL + tombstone, the
`alert_channel` pattern): `name`, `displayName`, `description`,
`retentionDays` (0 = inherit global), `archived`, `source` (`config`|`ui`),
`createdBy`, `updatedAt`. New `storage.Store` methods `ListProjects`,
`SaveProject`, `ArchiveProject`. The API-layer merge that today unions
`ListTenants()` (auto-discovery) with chart values gains this table as a third,
authoritative source; config-declared names render read-only.

**Retention.** Global retention today is `RetentionTracesDays` etc. in
`api.Config` and applied as table TTLs by the migrations. Per-project retention
needs a **partition/TTL scoped by `Tenant`** — the mechanism is a per-project
`TTL` expression is not directly per-value in a shared table, so v0.2 applies
per-project retention via a **scheduled hub job** that issues
`ALTER TABLE … DELETE WHERE Tenant = ? AND Timestamp < now() - INTERVAL n DAY`
(lightweight mutations, bounded, off-peak) for projects whose `retentionDays`
differs from the global TTL. The global TTL stays the coarse backstop; the job
only trims projects wanting *shorter* windows. (Longer-than-global is a documented
non-goal for v0.2 — it would fight the table TTL.)

**Status.** `handleSystemStatus` (`api/system_status.go`) already computes
retention + per-signal presence; parameterize it by the resolved `project(r)` so
the Settings → System page reflects the selected project, adding an ingest-rate
and storage-estimate figure from `system.parts`/`system.columns` filtered by
`Tenant`.

**Chart toggles.** Chart values `components.{hub,ui,gateway,sensor,clickhouse}.enabled`
+ `clickhouse.external.{dsn,secretRef}`. A gateway(+sensor)-only render omits the
hub/ui/clickhouse subcharts and points the exporter at the external DSN. The
existing per-signal/per-namespace collection knobs are unchanged. Upholds the
[wedge](../AGENTS.md): the default all-in-one install is untouched; toggles are
opt-in for the multi-cluster case.

### Alternatives considered

- **Per-project tables/databases** — clean isolation, but explodes schema and
  breaks the single-storage-engine promise; the `Tenant` column already isolates.
- **ClickHouse row-level TTL per project via a `TTL` column expression** —
  ClickHouse TTL can't parameterize by an arbitrary per-tenant window without
  per-partition TTLs; partitioning by tenant hurts the common cross-tenant-free
  query path. The bounded mutation job is simpler and touches only opt-in projects.
- **Project CRUD in a config file only** — keeps the hub stateless but forces a
  redeploy per project; the AEP for auth already made ClickHouse the config+UI
  hybrid store, so reuse it.

## Verification

- **Unit**: project merge (config vs UI vs discovered, precedence + read-only
  flags); retention-job SQL builder; status parameterized by project.
- **Integration (ClickHouse)**: project store FINAL semantics; the retention
  mutation deletes only the target tenant's aged rows.
- **e2e (compose)**: create a project in the UI, send data tagged to it, see it
  in the switcher and status; set a 1-day retention and verify older rows are
  trimmed; a config-declared project is read-only.
- **Helm**: `helm template` renders a gateway-only install against an external
  ClickHouse DSN; the default all-in-one render is unchanged.
- **Done** = an admin creates/rename/archives projects in the UI, a project can
  carry a shorter retention than the global default, status reads per project,
  and a secondary cluster installs gateway-only against a shared ClickHouse.

## Roadmap

- [ ] AEP accepted
- [ ] `projects` table + store methods + API merge (config/UI/discovered)
- [ ] Project CRUD API (Admin) + UI (Settings → Projects)
- [ ] Per-project retention setting + scheduled trim job
- [ ] Per-project system status (ingest rate + storage estimate)
- [ ] Chart component toggles + external-ClickHouse values
- [ ] e2e + helm template tests; docs-align (EN/FR)
