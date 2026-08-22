# AEP: Green — per-service energy and carbon attribution

- **Date:** 2026-07-22
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Add a **green** module: per-pod energy measurement via **CNCF Kepler** (RAPL),
correlated with the traces and metrics avuru-obs already collects, producing
**Wh and gCO2e per service / deployment** and **Wh per request** (with
mgCO2e/request derived via the carbon factor), monthly **carbon budgets**
per team with webhook alerts, and a **CSRD-ready export** with the methodology
stated. Self-hosted, zero-code, Helm-installed — no data leaves the cluster and
no external API is called. Kepler joins the sensor DaemonSet as the fourth
pinned upstream container; a **new prometheus receiver** in the sensor's
otel-agent scrapes it onto the **existing** OTLP → gateway → `otel_metrics_*`
path, and the hub computes energy and carbon at query time. No new ClickHouse
tables, no migration.

## Motivation

Teams are being asked for per-application energy and carbon numbers — ESRS E1 /
CSRD reporting, internal sustainability targets, cost-of-compute pressure — and
the answer today is either a spreadsheet estimate or a SaaS that wants the
cluster's telemetry. avuru-obs already knows which pod belongs to which service,
which service handled which requests, and what the CPU did; Kepler adds the one
missing measurement (joules from RAPL), and the correlation the platform already
does turns it into *per-service* Wh, *per-request* intensity, and gCO2e a report
can cite.

Ties to the [wedge](../AGENTS.md): zero app changes, reuse of the sensor
DaemonSet and the map/RED data already collected. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
reuse over rewrite (a pinned upstream collector, no bespoke eBPF), OTLP-native
metrics into the existing `otel_metrics_*` tables, the hub reads via SQL, and
the module framework gates the whole surface.

### Goals

- **Per-service energy and carbon**: Wh and gCO2e per service / deployment over
  a window, zero code changes, data never leaves the infrastructure.
- **CSRD-ready export**: per-app numbers for ESRS E1 with a **methodology
  block** — formula, factor provenance, coverage ratio, and the unattributed
  bucket — so an auditor can see exactly how a number was produced.
- **Carbon budgets**: monthly gCO2e budgets per `serviceGroups` group, with
  **warn at 80%**, **exceeded at 100%**, a month-end projection, and webhook
  delivery through the alerting module's existing channels.
- **Per-request intensity**: Wh ÷ RED request count, with mgCO2e/request
  derived via the carbon factor — in the spirit of the Green Software
  Foundation's **Software Carbon Intensity (SCI)**, though ours is energy per
  request where SCI proper is carbon per functional unit. Efficiency work
  shows up even as traffic grows.
- **Trend per service**, to prove an optimization actually reduced energy.
- **Service-map overlay**: gCO2e as a lens on the map the operator already uses.
- **Per-tenant reporting** via the existing `Tenant` column — no new seam.
- **Air-gap friendly**: bundled per-country annual-average intensity factors,
  operator overrides in config, zero egress.

### Non-goals (v1)

- **Hourly / live grid intensity** — no external APIs; static factors only
  (see Alternatives).
- **Scope 3 / embodied carbon** — operational (Scope 2-shaped) energy only.
- **Per-endpoint attribution** — per-service and per-request; not per-route.
- **CRUD budgets** — budgets are config-defined like alerting rules; UI
  authoring wants the auth seam.
- **TDP-based estimation for RAPL-less nodes** — a post-v1 roadmap item; v1
  reports honestly only what was measured. Now designed in
  [AEP 2026-07-28](2026-07-28-green-tdp-estimation.md).
- **RBAC on carbon data** — rides the auth/OIDC module when it lands (roadmap
  note); v1 exposes green data to any hub user like every other module.

## Solution

A module declared in the [registry](../hub/internal/modules) and gated across
the usual layers: `modules.green.enabled`, an `AVURUOPS_MODULES` entry `green`,
`/api/v1/green/*`, UI `/green`. **Green requires `infra-metrics`** (the
pod→workload join reads kubeletstats attributes) — enforced fail-loud in
`modules.Parse` and by a Helm `{{ fail }}` guard. That is the **only hard
dependency**: green does **not** require `alerting`. With `green` on and
`alerting` off, budgets degrade to dashboard/UI status only —
used/projected/ratio/status are still computed at query time — while
notifications and alert-history persistence simply don't happen; this
degradation is documented, not an error.

**Born opt-off**: `modules.green.enabled: false` **and**
`sensor.green.enabled: false` by default. The signal is hardware-dependent
(RAPL); a default-on module would silently flip on for every existing install
on the next upgrade.

### Data flow

```
RAPL (powercap) ── Kepler (pinned v0.11.x, 4th sensor container, opt-in) ──▶ :metrics
        │
  [otel-agent prometheus receiver, keep-regex kepler_(node|pod)_cpu_(watts|joules_total)]
        │ OTLP
        ▼
  gateway ──▶ otel_metrics_sum / otel_metrics_gauge     (existing tables, no migration)
        │
  hub green module: read-time Wh + gCO2e aggregation, budgets evaluator
        ├──▶ GET /api/v1/green/* ──▶ /green UI, service-map overlay, CSRD export
        └──▶ budget notifications via the alerting Notifier/channels + alert_state/history
```

### Collection (sensor)

**CNCF Kepler** — the rebooted line (≥ v0.10), pinned at **v0.11.x** — runs as
the fourth pinned upstream container in the sensor DaemonSet, opt-in via
`sensor.green.enabled`. A **new prometheus receiver** in the otel-agent scrapes
its Prometheus endpoint; the existing OTLP path carries it to ClickHouse.
`sensor.green.enabled` deliberately deviates from the component-named precedent
(`sensor.obi`, `sensor.profiler`): the block also carries green-specific scrape
config, and the module-named flag pairs with `modules.green`. No bespoke eBPF:
the repo principle
is **reuse over rewrite**, and energy attribution is exactly the kind of
kernel-adjacent measurement an upstream CNCF project should own.

The Kepler container gets **no liveness/readiness probes**: on a RAPL-less node
it may sit unhealthy, and it must not flap the sensor pod — the sensor
"do no harm" probe-canary gate (`deploy/helm/e2e-helm.sh`) and the TTV < 300 s
wedge gate (`e2e/wedge_test.go`) are hard CI gates this design must not disturb.

### Metric names (single source of truth)

Both halves cite this table: the hub's config defaults and the sensor's
keep-regex must match it, and nothing else may hardcode a Kepler metric name.

| Metric | What it is | Attributes | Read by v1 hub? |
|---|---|---|---|
| `kepler_pod_cpu_joules_total` | cumulative pod CPU energy (J) | `pod_name`, `pod_namespace` | yes |
| `kepler_node_cpu_joules_total` | cumulative node CPU energy (J) | — | yes |
| `kepler_pod_cpu_watts` | instantaneous pod CPU power (W) | `pod_name`, `pod_namespace` | collected for future use, not queried |
| `kepler_node_cpu_watts` | instantaneous node CPU power (W) | — | collected for future use, not queried |

- Sensor keep-regex: `kepler_(node|pod)_cpu_(watts|joules_total)`.
- Pod→workload join: the kubeletstats `k8s.pod.cpu.usage` ResourceAttributes
  (owner precedence deployment > statefulset > daemonset) — hence the
  `infra-metrics` requirement.
- **All metric names are config-overridable in the hub**, because
  rebooted-Kepler naming is a verify item (see Documented v1 limitations).

### Storage — no new tables

Read-time aggregation over `otel_metrics_sum` / `otel_metrics_gauge` (the
gauge read is the kubeletstats pod→workload join — `k8s.pod.cpu.usage`
ResourceAttributes — not Kepler watts, which v1 never queries), the
service-health / network-health precedent: Wh = Δjoules ÷ 3600 from the
cumulative counters per pod over the window, summed to the workload via the
kubeletstats join. Measured pod energy the join cannot place is reported as an
explicit **unattributed** bucket, and attributed ÷ measured is the **coverage
ratio** the methodology block cites — an *attribution* ratio over what was
measured: a RAPL-less node emits no counters at all, so it moves neither side
of the ratio (see the degradation story below). Metrics retention already
covers the data; no migration, no rollup jobs.

### CO2e factors — static config, computed at query time

`gCO2e = Wh × intensity/1000 × PUE`, computed in the hub at query time.
Grid intensity (gCO2e/kWh) and PUE are **operator-set per cluster** in config,
with **bundled per-country annual averages** as defaults. No external API, zero
egress. Factor provenance (operator-set vs bundled default and its vintage) is
part of every export's methodology block.

### Budgets and alerts

Budgets are **config-defined** (a hot-reloaded ConfigMap, the `serviceGroups` /
alerting-rules pattern), not CRUD: a monthly gCO2e budget per `serviceGroups`
group, with warn (80%), exceeded (100%), and a month-end projection.

Delivery reuses the alerting module's **persistence + delivery seam** —
`alert_state`, `alert_history`, the `Notifier` interface and configured
channels — with **zero changes inside `hub/internal/alerting/`**: green owns a
**pure evaluator** (the `Evaluate` shape: state in, state + notifications out)
and is **merged into the same evaluation tick** in `hub/cmd/hub/alerting.go`.
A second independent ticker persisting to `alert_state` would race the existing
loop's `diffToSave` ok-supersede path and could clobber a firing row — a
verified hazard in that file — so one tick evaluates both.

### Budget lifecycle (per budget, each tick)

Each budget yields **two logical rules**, keyed `green:<budget>:warn` and
`green:<budget>:over`, target `group:<group>`, each using the **existing**
`ok`/`firing` states of the `alert_state` enum — hence no migration and zero
edits inside `hub/internal/alerting/`. A rule **fires once per crossing** (warn
at 80% of budget, over at 100%) and **resolves on month rollover** or when
usage drops back below its threshold. Green **imports** alerting types
(`Notification`, `State`); the reverse import never happens. Green's evaluation
merges its next-state into the existing tick's next-state **before**
`diffToSave`/delivery — closing the ok-supersede hazard named above.

### API and UI

- `GET /api/v1/green/*` — energy/carbon per service and group, budget status,
  the CSRD export (numbers + methodology block), all tenant-scoped and
  module-gated.
- `/green` UI — carbon dashboard (per-service Wh/gCO2e, trend, per-request
  intensity), budget status, export download; a gCO2e overlay on the service
  map. Capability-gated via `ModuleGate`.

### Degradation story (no RAPL, no data)

Most public-cloud VMs expose no powercap/RAPL — v1 targets bare-metal / metal
instances. On clusters without RAPL: a **teaching empty state** in `/green` and
a **preflight warning**, never a block; the Kepler container (no probes) must
not affect the sensor pod's health. On a partial fleet (some nodes with RAPL,
some without) the RAPL-less share is *absent* energy — workloads there simply
report nothing, and the coverage ratio (attribution over measured energy)
cannot reveal it. Node-based coverage — nodes reporting energy vs known nodes,
from the already-collected node counters — is the follow-up that makes that
gap explicit (see roadmap follow-ups).

### Upholds the enterprise seam

`Tenant` leads every green query; factors and budgets are per-install config
today (per-tenant factors couple to the auth seam later); storage stays behind
`storage.Store`; retention is the existing metrics policy.

### Alternatives considered

- **Extend `alerting.Evaluate`'s input to carry carbon conditions** — rejected:
  it forces budget semantics into the health-shaped state machine and means
  editing `hub/internal/alerting/`. A green-owned pure evaluator reusing only
  the persistence + delivery seam keeps the alerting module untouched and the
  budget logic independently unit-testable.
- **An owned rollup table (pre-aggregated Wh)** — rejected: a migration, a
  writer job, and a second source of truth for data the existing
  `otel_metrics_*` tables already hold at adequate query cost; read-time
  aggregation is the proven service-health / network-health pattern.
- **A live grid-intensity API** — rejected: egress from a self-hosted,
  air-gap-friendly product, an availability dependency in the read path, and
  hourly factors change reported numbers retroactively. Static operator-set
  factors with bundled annual defaults are auditable and offline.
- **Bespoke eBPF energy collection in the sensor** — rejected: reuse over
  rewrite; Kepler is the CNCF-maintained implementation of exactly this.

### Documented v1 limitations

- **Kepler config keys, metric names/labels, port and RBAC sufficiency are
  unverified against the pinned rebooted Kepler in a RAPL environment — must be
  confirmed on real hardware before prod use; CI validates the path via
  Kepler's dev fake-cpu-meter.** Hub metric names are config-overridable for
  this reason.
- **No RAPL → no data**: cloud VMs generally report nothing; v1 does not
  estimate (no TDP fallback yet — post-v1).
- **Static annual factors**: reported gCO2e uses annual-average intensity, not
  the grid's hour-by-hour reality; the methodology block says so.
- **Counter deltas are an approximation** across pod churn and counter resets,
  the same caveat as the other cumulative-counter reads; the unattributed
  bucket absorbs what the join cannot place.

## Verification

- `make check` — unit: Wh/gCO2e math, factor config parsing (fail-loud), the
  budget evaluator state machine (warn → exceeded, projection, dedup, resolve),
  module dependency enforcement in `modules.Parse`.
- Hub `make test-int` (testcontainers ClickHouse) — the green SQL: seeded
  `kepler_*` rows produce correct per-workload Wh, coverage ratio, and the
  unattributed bucket.
- `make helm-check` — `green` in `AVURUOPS_MODULES`, the sensor container and
  scrape config render only when opted in, the `{{ fail }}` guard fires without
  `infra-metrics`, everything disappears when disabled.
- `make e2e` (compose + seeded Kepler metrics) — dashboard, budgets, export;
  the budgets endpoint surfaces the seeded budget over the mounted config
  (the seeded usage never crosses a threshold, so no webhook fires — delivery
  is covered by the unit tests and channel validation).
- `make e2e-ui` — Playwright: `/green` renders from seed; disabled-module path.
- `make e2e-helm` — Kepler runs with its **dev fake-cpu-meter**; `kepler%` rows
  polled in ClickHouse; the **TTV and probe-canary gates unchanged**.
- **Post-merge**: confirmation on real RAPL hardware (roadmap item below) —
  blocks prod use. The procedure is written down:
  [docs/runbooks/green-rapl-validation.md](../docs/runbooks/green-rapl-validation.md).

## Roadmap

- [x] AEP accepted
- [x] Sensor: Kepler (pinned v0.11.x) as 4th DaemonSet container + prometheus
      scrape + keep-regex, opt-in, no probes
- [x] Hub: `green` module registry entry + `infra-metrics` dependency in
      `modules.Parse`
- [x] Hub: read-time energy/carbon queries + factor + budget config (hot-reload)
- [x] Hub: budget evaluator merged into the alerting tick (reusing
      `alert_state`/`alert_history`/`Notifier`; zero `internal/alerting` edits)
- [x] API `/api/v1/green/*` + UI `/green` + service-map overlay + CSRD export
- [x] Deploy: `modules.green` + `sensor.green` values, `{{ fail }}` guard,
      schema, template-test
- [x] e2e (seeded) + Playwright + e2e-helm fake-cpu-meter leg (TTV + canary
      gates untouched)
- [ ] **Confirm Kepler config keys, metric names/labels, port and RBAC on real
      RAPL hardware** (blocks prod use) — runbook written
      ([green-rapl-validation](../docs/runbooks/green-rapl-validation.md));
      the box closes when the run happens, not when the runbook lands
- [ ] Docs (module page, factors/budgets configuration, methodology) via
      docs-align
- [ ] Post-v1: TDP estimation for RAPL-less nodes
      ([AEP 2026-07-28](2026-07-28-green-tdp-estimation.md), Draft), hourly
      intensity, per-endpoint attribution, CRUD budgets + RBAC via the auth
      module

### Post-v1 follow-ups (from the branch review)

- [x] Per-budget deliverability signal on the budgets DTO — `notifications`
      (ok | alerting-off | no-channel | unknown-channel) resolved from the
      modules, the file-config channels and the UI-stored ones; the card
      footnote names the actual fix instead of only "alerting is off".
- [x] Warn at runtime when a budget names a serviceGroups group that doesn't
      exist — `warnings` on the budgets response plus an hourly-throttled
      tick log, both from one check so the two never disagree. "Known" is the
      configured groups UNION the groups services actually landed in, so
      namespace auto-groups stay legitimate targets.
- [x] Node-based coverage: `NodeEnergy()` now backs a per-node table on the
      green summary (`nodes[]`, with quality), so a RAPL-less share is
      visible per node rather than only as unattributed energy.
- [ ] Extract a generic hot-reload config loader — green's loader is the third
      copy of the groups.go pattern.
