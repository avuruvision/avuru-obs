# AEP: Service health groups — consolidated group health from RED

- **Date:** 2026-07-18
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Add a **service-health** module that rolls per-service **RED** health up into
consolidated **group** health. Each service gets a status (`healthy | degraded |
down | idle`) from the rate/errors/duration the hub already derives from traces;
services are grouped — by config selector or, failing that, automatically by
Kubernetes namespace — and tagged with a criticality **tier** (T0/T1/T2). A
group's status is the worst of its members, and a **dependency-propagation** rule
means a service can't read green while a *critical* dependency is red. Health is
purely **derived** (no probing, no new schema), so the hub stays out of the
telemetry byte-path. Ships as the next module born on the
[module framework](2026-07-15-module-framework.md), after
[error tracking](2026-07-16-error-tracking.md).

## Motivation

avuru-obs has no health concept for *user* services today. `/api/v1/system/status`
reports on avuru-obs's own components (Hub, ClickHouse, Ingestion); health-check
traffic is deliberately dropped as aux noise; and "projects" are tenants
(environment isolation), not a way to group services *within* an environment. An
operator can see a services table and a RED dashboard, but not "is the checkout
group healthy right now, and if not, which service — or which of its critical
dependencies — is the cause?"

Who feels it:

- **SRE / on-call** want a single board that answers "what's broken and how bad"
  at the level they think in — business capabilities and criticality tiers — not
  a flat per-service list. The golden rule they carry in their heads ("a service
  can't be healthy if a T0 dependency is down") should be encoded, not
  re-derived by eye from the service map.
- **The product** gets a status surface that generalizes: it extends the
  self-status idea to user services, per project, and defines the read-side
  status model that a future **alerting** module fires on.

Ties to the [wedge](../AGENTS.md): no app changes, works off the trace data the
sensor already collects zero-code. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
health is computed by SQL over ClickHouse at read time; the hub never originates
traffic to user services (no prober). *Environment* is **not** a new dimension —
the gateway stamps one `avuru.tenant` per cluster, so prod/staging/dev is the
existing project switcher; **tier** is the one new grouping axis, within a project.

### Goals

- A per-service status from RED over the active window: an error budget
  (`warn`/`crit`) and a p95 latency objective, with a traffic gate first so a
  quiet window reads **idle**, never `down`. Thresholds configurable with
  precedence **service > tier > global default**. Every status carries a
  human reason (`"error rate 4.2% ≥ 1% budget"`).
- **Hybrid grouping**: config selectors (by namespace or explicit service name)
  assign services to named groups with a tier; unmatched services auto-group by
  their k8s namespace at a default tier. Zero-config gives a useful board;
  config refines it.
- **Dependency propagation**: dependencies are auto-discovered from the existing
  service-map edges (`ServiceEdges`); an edge `s → t` is **critical iff `t` is
  T0** (or a configured override). A critical dependency being `down` worsens
  the dependent to at least `degraded`. Cycle-safe and explainable.
- Gated by the module framework: `modules.serviceHealth.enabled` (default
  **true** — it's free value from existing data; disabling only removes the API
  routes + UI, never any ingestion weight).
- Config editable without a full redeploy: rendered to a ConfigMap the hub
  hot-reloads.

### Non-goals (v1)

- Alerting/notification on status transitions — needs the notification subsystem
  avuru-obs doesn't have yet; this AEP defines the status model it will consume.
- **Transitive** (multi-hop) dependency propagation, and `degraded`-dependency
  propagation — v1 is one-hop, `down`-only.
- Cross-tier "capability" groups with per-tier severity caps, and a
  `groupBy=capability` pivot — v1 groups are single-tier, so plain worst-of is
  exact.
- Burn-rate / multi-window SLOs, flap damping, true group-quantile latency
  (v1 reports the worst-member p95 as the group headline).
- Auto-suggesting which dependencies are critical from observed topology.

## Solution

A module named in the [registry](../hub/internal/modules) and honoured across the
same layers as every other module — but it **owns no schema/migration**: health
is a pure query over `core` trace data (a migration-less module is legal; the
migrator only iterates tagged migrations, and the coverage test never asserts
module→migration).

### Health logic (`hub/internal/health`, pure — no SQL, no I/O)

- `status.go` — per-service rule (§Goals) + severity ordering (`worst`). Idle
  and unknown sit below healthy so they never drag a group down.
- `resolve.go` — service→group assignment: first matching config selector wins
  (`source: "config"`), else auto-group by namespace
  (`k8s.namespace.name` → `service.namespace` → `(unlabeled)`, `source: "auto"`).
- `rollup.go` — RED aggregation per group, group status = worst-of member
  *effective* status (idle excluded; all-idle → idle), and the propagation pass.
- `config.go` — the config model with `ParseConfig`/`Validate`/`Default`,
  fail-loud like `modules.Parse`, and threshold resolution.

**Propagation is cycle-safe by construction**: a single pass over the edges reads
each dependency's *base* status (never its *effective* status) and never
recurses, so even a mutual `A ↔ B` call graph evaluates in `O(edges)` and
terminates. It only worsens (green → degraded), never improves and never forces
`down`; an idle service stays idle but is annotated. Both `baseStatus` and
`effectiveStatus` are returned with the reason, so the UI can say "healthy on its
own, degraded because payments is down."

### Storage seam (one new read method)

`ServiceLabels(ctx, ServiceQuery)` returns each service's dominant grouping
namespace via `argMax(label, count())` over the same entry-span population as
`ListServices`, so the service sets line up (`ResourceAttributes` bloom indexes
keep it cheap). `ListServices` (RED) and `ServiceEdges` (topology) are reused
unchanged. No new table.

### API (hub)

`GET /api/v1/health/groups?start&end[&includeAux]` returns
`{ overall, checkedAt, window, groups[] }`; each group carries its status,
rollup reason, per-status counts, aggregate RED, and members. Each member
carries base + effective status, the reason, its RED evidence, and its critical
dependencies (each with the dependency's own status and a `critical` flag) — so
the "why" chain renders with no second call. `GET /api/v1/health/groups/{name}`
drills into one group. Routes registered only when the module is active;
tenant-scoped like every endpoint.

### Config source (hot-reloadable ConfigMap)

`serviceGroups` in Helm values (defaultTier, groups, thresholds, criticalEdges)
renders via `toJson` to a `-groups` ConfigMap, mounted at `AVURUOPS_GROUPS_CONFIG`.
Absent → `Default()` (auto-group by namespace), mirroring an empty
`AVURUOPS_PROJECTS`. The hub parses fail-loud at startup and **hot-reloads** on
mtime change (a bounded poll into an `atomic.Pointer`, no new dependency), so a
`kubectl edit` of the ConfigMap re-tiers services **without a pod restart or a
Helm redeploy** — reclassification is a one-line change. `values.schema.json`
validates the shape (tier enums fail `helm template` fast).

### UI

`/health`: a board of **tier lanes** (T0/T1/T2 + Unassigned), each a grid of
group cards (status, rollup reason, per-status tally, member chips). Clicking a
member opens a detail panel — base-vs-effective status, RED evidence, and the
critical-dependency chain — with links to the service map and the service's
traces. Capability-gated via `ModuleGate`; `idle` renders neutral (not the amber
`system-status` uses) so "no traffic" doesn't read as a warning.

### Upholds the enterprise seam

Every query is `Tenant`-scoped via `X-Avuru-Tenant`; the module owns no table so
there is no retention knob to add; storage stays behind `storage.Store`. An
optional per-group tenant allowlist for shared-ClickHouse installs is a later item.

### Alternatives considered

- **Active probing** (call each `/healthz` on a schedule) — puts the hub in the
  byte-path as a traffic originator (a rejected role) and needs a target
  registry + scheduler. Deriving from RED reuses data already collected and
  keeps the hub API-only.
- **Kubernetes readiness as the health source** — covers only k8s workloads and
  reflects pod liveness, not whether real traffic is served well; RED does both.
- **Core, not a module** — health is `core`-derived, so it *could* be always-on;
  a module gives small installs a clean 404 toggle and matches the framework's
  born-opt-in direction, at no cost (no schema).
- **Group-level declared dependencies** — modeling deps as service→service and
  auto-discovering them from `ServiceEdges` (criticality = target tier) means no
  manual dependency wiring, and reclassifying a service updates its grouping and
  its criticality-as-a-dependency at once.
- **Config as inline env JSON** — too rich a shape for an env var and no schema
  validation; a ConfigMap keeps Helm's `values.schema.json` and enables
  hot-reload.

### Documented v1 limitations

- **One-hop propagation**: a service three hops above a down leaf is not
  worsened unless it has a *direct* critical edge to a down service (reading
  base, not effective, is what guarantees termination). Transitive closure is
  deferred.
- **Group latency is the worst-member p95**, not a true group quantile (summing
  per-service quantiles is not a quantile); a dedicated `quantiles()` query is
  deferred.
- **Unlabeled services** (pure-SDK apps setting neither namespace attribute)
  fall into an `(unlabeled)` auto-group until config names them explicitly.

## Verification

- **Unit** (`hub/internal/health`, `hub/internal/api`): the status rule across
  thresholds; hybrid grouping (config selector + namespace auto-fallback);
  propagation incl. a **cyclic** graph that must terminate; idle short-circuit;
  config validation fail-loud; the handler over `storagetest.Fake` (grouping,
  tier passthrough, propagation, 503 on store outage, tenant header). Plus the
  module-gating updates to `capabilities_test.go` / `router_test.go`.
- **Helm** (`deploy/helm/template-test.sh`): `service-health` renders into
  `AVURUOPS_MODULES`, the groups ConfigMap + env + mount render, and disabling
  the module removes the whole surface; `values.schema.json` rejects a bad tier.
- **Integration** (testcontainers ClickHouse): `ServiceLabels` returns the
  dominant namespace per service over the entry-span population.
- **e2e / Playwright** *(follow-up)*: seed services across namespaces with known
  error rates; assert group statuses, tier lanes, and one propagation case (a
  green service reads `degraded` because its seeded T0 dependency is `down`);
  Playwright drives the board → member detail → dependency chain, plus the
  disabled-module 404.
- **Manual**: `kubectl edit` the groups ConfigMap and confirm the board re-tiers
  without a pod restart.

## Roadmap

- [x] AEP accepted
- [x] Hub: `service-health` module registry entry + `ServiceLabels` seam + the
  `hub/internal/health` logic package
- [x] Hub: `GET /api/v1/health/groups[/{name}]` + hot-reloadable config loader
- [x] Helm: `modules.serviceHealth`, `serviceGroups` ConfigMap, env/mount, schema,
  template-test
- [x] UI: `/health` tier-lane board + member detail + dependency chain, module-gated
- [x] e2e (compose + seeded fixtures) + Playwright coverage
- [ ] Docs: signal/module page (EN/FR), configuration reference, API reference,
  ROADMAP + docs-site roadmap
