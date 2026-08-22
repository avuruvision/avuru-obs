# AEP: Projects completion — CRUD, per-project retention, status, chart toggles

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Accepted — fully delivered. Phase 1 (project CRUD) shipped in
  v0.3; member projects, per-project retention, per-project status and chart
  component toggles shipped in v0.6. Phase 1 delivery design:
  [project-management combined spec](../docs/superpowers/specs/2026-07-28-project-management-design.md).

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

- [x] AEP accepted
- [x] `projects` table + store methods + API merge (config/UI/discovered) — v0.3
- [x] Project CRUD API (Admin) + UI (Settings → General) — v0.3
- [x] **Member projects** (multi-cluster aggregation) — v0.6, PR #106
- [x] Per-project retention setting + scheduled trim job — v0.6, PR #108
- [x] Per-project system status (ingest rate + storage estimate) — v0.6, PR #109
- [x] Chart component toggles + external-ClickHouse values — v0.6, PR #110
- [x] e2e + helm template tests; docs-align (EN/FR)

## What shipped, where it differs from this AEP

Four decisions moved between writing this and building it. Each is recorded
here rather than in a commit message, because each is the kind of thing a
future reader would otherwise re-litigate.

**Member projects were not in the original scope.** The AEP treated a project
as an isolation boundary and listed cross-project aggregation as a non-goal.
v0.6 added it anyway, as a READ-TIME union: `resolveTenants` expands membership
once at the request edge and every store query fans out with `Tenant IN (?)`.
The non-goal it actually preserved is the important one — an aggregate is a
convenience over existing permissions, never a way around them: each viewer
sees only the members they were already granted, and membership is one level
deep so an expansion can never miss a subtree.

**Retention is one number per project, not per signal.** The AEP said "a
retention-days setting per project"; the temptation was to mirror the five
global per-signal windows. One number won: a project that wants less wants
less of everything, and five numbers would multiply the mutation surface by
five for a distinction nobody asked for.

**Longer-than-global is refused, not just documented.** The AEP called it a
documented non-goal. In practice a stored 90 on a 7-day install is a number
that changes nothing — the shared table TTL drops the rows first — so the API
answers 400 and names the ceiling instead of accepting a promise it cannot
keep. The trimmer only ever deletes EARLIER than the install would.

**Per-project bytes are an estimate, and say so.** The AEP suggested reading
`system.parts`/`system.columns` filtered by `Tenant`. That filter does not
exist: parts hold every tenant's rows together, and our tables partition by
date, not tenant. So rows, time bounds and ingest rate are counted exactly with
the tenant filter, and bytes are apportioned by row share — surfaced as
`estimatedBytes`, labelled "Size (est.)" with the reason in a footnote rather
than printed as a measurement.

**Writes on an aggregate answer 409.** Not in the AEP because aggregates were
not either, but it follows from the same rule: an aggregate owns no rows, so
every per-tenant WRITE (error triage, ingest keys, profile ingest, a retention
window) has no single tenant to land in. Each refusal names the member project
to use instead. Per-member fan-out writes are a later decision.
