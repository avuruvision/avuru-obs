# AEP: Green TDP estimation — modeled energy for RAPL-less nodes

- **Date:** 2026-07-28
- **Author(s):** Berny ryders
- **Status:** Accepted — targeting v0.3. Implementation decisions (coefficient
  sourcing, component layout, release sequencing) recorded in the
  [combined spec](../docs/superpowers/specs/2026-07-30-green-tdp-estimation-design.md).

## Summary

The [green module](2026-07-22-green-carbon.md) reports only what RAPL measures,
and most virtualized nodes expose no RAPL — on the clusters most people actually
run, `/green` shows its teaching empty state and nothing else. This AEP adds the
promised post-v1 fallback: an **opt-in TDP-based estimator** — a small exporter
in the sensor DaemonSet that activates **only on RAPL-less nodes** and emits the
**same Kepler metric names** from a power model
(`P = P_idle + u × (P_max − P_idle)`), every series stamped
`avuruops_quality="estimated"`. The keep-regex, the OTLP path, the
`otel_metrics_*` tables, the pod→workload join, and the read-time aggregation
pattern are unchanged; the hub threads a **measured / estimated / absent**
quality split through the SQL, the API, the UI, and the CSRD export's
methodology block. The v1 honesty stance is preserved: estimates are labeled
everywhere and never silently blended with measurements.

## Motivation

Green v1 deliberately chose *measured or nothing* — and documented TDP
estimation as the post-v1 roadmap item. The result is correct and useless on
VMs: a team on virtualized nodes gets an empty dashboard, no trend, no budget
signal, nothing to cite. Estimation with stated error bars is how the field
answers this (SPECpower-derived coefficient models are the standard methodology
for exactly this gap), and it is what those teams ask for: directional
per-service numbers to find regressions and drive optimization, not
audit-grade joules.

Ties to the [wedge](../AGENTS.md): zero app changes, and the module starts
working on the infrastructure people actually have instead of requiring a
hardware migration first. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
same OTLP-native path into the existing `otel_metrics_*` tables, hub reads via
SQL, no new tables, module framework gates the surface. And it ties to green's
own founding principle: the label — not the absence of data — is what keeps the
numbers honest.

### Goals

- **Modeled Wh and gCO2e on RAPL-less nodes**, opt-in, labeled *estimated*
  end-to-end: SQL → API DTOs → UI badge → export methodology.
- **Three quality tiers** — measured / estimated / absent — with **node-based
  coverage** (known nodes vs nodes reporting each tier), closing the
  [green-carbon follow-up](2026-07-22-green-carbon.md#post-v1-follow-ups-from-the-branch-review)
  that made the RAPL-less share invisible.
- **Per-node coefficient resolution** with provenance: operator values first,
  bundled per-CPU-model table second, generic per-core fallback last — and loud.
- **A pluggable estimator input**: the TDP curve now; a hypervisor-assisted
  feed (real host RAPL attributed to VMs) later, behind the same output shape.

### Non-goals

- **Replacing measurement.** Where RAPL exists, Kepler measures and the
  estimator stays dormant; a node never reports both.
- **Audit-grade accuracy.** The model's typical absolute error is ±30–50 %;
  good for trends, comparisons, and regressions — stated as such everywhere,
  never presented as reporting-grade.
- **GPU / DRAM power modeling** — CPU package power only, matching v1's scope.
- **Hourly grid intensity** — unchanged, its own roadmap item.
- **Upstreaming into Kepler** — a watch item, not a goal (see Alternatives).

## Solution

```
RAPL present ── Kepler (4th container, unchanged) ──▶ kepler_* series ── stamped avuruops_quality="measured"
RAPL absent  ── tdp-estimator (5th container, opt-in, dormant if RAPL) ──▶ same kepler_* names, avuruops_quality="estimated"
        │
  [same otel-agent prometheus receiver, same keep-regex kepler_(node|pod)_cpu_(watts|joules_total)]
        │ OTLP
        ▼
  gateway ──▶ otel_metrics_sum / otel_metrics_gauge      (existing tables, no migration)
        │
  hub green module: quality-aware Wh + gCO2e, node coverage (measured / estimated / absent)
```

### The estimator

A small first-party Go exporter, the fifth sensor DaemonSet container, opt-in
via `sensor.green.estimation.enabled` (requires `sensor.green.enabled`; guarded
in values schema and template). Like Kepler it gets **no liveness/readiness
probes** — it must never flap the sensor pod; the probe-canary and TTV CI gates
are untouched.

- **RAPL probe at startup**: if `/sys/class/powercap/intel-rapl*` is readable,
  the estimator goes dormant — it serves an empty `/metrics` and logs why. A
  node's power interface doesn't change under a running pod; a reboot restarts
  the DaemonSet pod and re-probes.
- **Sampling**: node utilization from `/proc/stat`, per-pod CPU from cgroup v2
  `cpu.stat` (the same accounting kubelet reads), on a fixed interval aligned
  with the scrape.
- **Model**: `P_node(t) = P_idle + u(t) × (P_max − P_idle)`; node joules are the
  integral over samples. Per-pod joules split only the **dynamic** part
  (`P − P_idle`) proportionally to pod CPU share; idle power stays node-only
  and lands in the existing **unattributed** bucket — no pod caused the idle
  draw, and pretending otherwise would corrupt per-service comparisons.
- **Output**: the exact metric table from the
  [green-carbon AEP](2026-07-22-green-carbon.md#metric-names-single-source-of-truth)
  — `kepler_{node,pod}_cpu_{watts,joules_total}` with `pod_name` /
  `pod_namespace` — plus `avuruops_quality="estimated"`. It passes the existing
  keep-regex untouched, joins to workloads through the same kubeletstats path,
  and lands in the same tables.

**Quality stamping**: the otel-agent's existing Kepler scrape gains an
attributes processor that stamps `avuruops_quality="measured"`, so the label is
always present on both sources and the hub SQL never infers quality from
absence.

### Coefficients — `P_idle`, `P_max`

Resolved per node, first match wins; the tier used and its provenance are
recorded per node and cited in the export methodology:

1. **Node annotation** `obs.avuru.io/power-idle-watts` /
   `obs.avuru.io/power-max-watts` — heterogeneous fleets.
2. **Helm values** `sensor.green.estimation.{idleWatts,maxWatts}` — uniform
   fleets; the accurate path for a LAN whose hardware the operator knows.
3. **Bundled per-CPU-model table** — `/proc/cpuinfo` model name matched against
   a comprehensive table derived from open SPECpower-based coefficient sets.
   Cloud Carbon Footprint's coefficient data (Apache-2.0) is the primary
   source; Boavizta's environmental-footprint-data is ODbL (share-alike for
   the database, not permissive as originally stated here) and is used only
   for a bounded number of specific, individually-cited facts filling CCF
   coverage gaps — never bulk-copied. Shipped and versioned exactly like the
   bundled grid-intensity factors (see
   [combined spec](../docs/superpowers/specs/2026-07-30-green-tdp-estimation-design.md#coefficient-table)).
4. **Generic per-core fallback** (per-vCPU idle/max averages × core count) —
   allowed but **loud**: a preflight warning and a methodology note, because
   its error band is the widest.

### Hub — quality-aware reads, no new tables

The green SQL gains `avuruops_quality` as a grouping dimension (it rides the
metric attributes in the existing rows — no migration). API DTOs split energy
and carbon into **measured** and **estimated** components; totals are shown as
both, never as one silently merged number. The node-coverage read the
green-carbon review asked for ships here: known nodes vs nodes reporting
measured vs estimated energy; a node reporting neither is **absent** — the gap
is finally visible instead of merely unattributed.

**Budgets include estimated energy** — a budget on an all-VM fleet (the very
audience of this AEP) would otherwise never move — but the budget status DTO
carries the estimated share, so a threshold crossed mostly on modeled energy is
visibly soft in the UI and in notifications' payload.

### UI and export

- `/green` shows an **estimated badge** wherever the estimated share is
  non-zero, and a **coverage panel** with the three tiers. On a RAPL-less
  cluster with estimation enabled, the preflight message flips from "no RAPL,
  no data" to "estimating via TDP model (±30–50 %)".
- The CSRD export's methodology block gains an **estimation subsection**: the
  formula, each node's coefficient tier and provenance, the coefficient-table
  vintage, and the error band — an auditor sees exactly which numbers are
  modeled and how.

### Upholds the enterprise seam

`Tenant` leads every green query, unchanged. Coefficients are per-install
config like the v1 carbon factors (per-tenant coefficients ride the auth seam
later, same note as v1). Storage stays behind `storage.Store`; retention is the
existing metrics policy; no migration.

### Alternatives considered

- **Hub-side estimation from kubeletstats** (no new container) — rejected: it
  forks the green read path into two SQL shapes, still needs a per-node CPU
  model inventory shipped from somewhere, and complicates the coverage math.
  Emitting Kepler-shaped series keeps one read path and one join.
- **Scaphandre as the estimator** — rejected for now: different metric names
  and attribution shape; mapping them plus reaching per-pod parity costs more
  than the small exporter it would replace. Its hypervisor/qemu mode — real
  host RAPL attributed per-VM — is the strongest candidate for the pluggable
  input seam later.
- **Wait for upstream Kepler VM/estimation support** — deferred, not rejected:
  the rebooted Kepler line may grow this; **re-verify the pinned Kepler before
  implementation starts**, and if upstream ships utilization-based estimation
  under the same names, the estimator shrinks to configuration.
- **Attribute full node power (incl. idle) to pods** — rejected: dishonest
  attribution; idle belongs to the node, consistent with v1's unattributed
  bucket.
- **Reuse Kepler's dev fake-cpu-meter** — rejected: synthetic, not
  utilization-derived; it is a CI/demo device by design.

### Documented limitations

- **±30–50 % typical absolute error**; suitable for trends and regressions,
  not for reporting-grade claims — the badge and the methodology block say so.
- **Linear utilization curve** — real power curves are convex; a curve exponent
  is a post-v1 refinement if side-by-side evidence demands it.
- **CPU package power only** — no DRAM/GPU/fan modeling; PUE still multiplies
  as configured.
- **cgroup v2 required** on estimator-enabled nodes (the per-pod split reads
  `cpu.stat`); cgroup v1 fleets are unsupported and documented as such.
- **Coefficient tables age**: the bundled set carries a vintage, cited in the
  export, and overrides always win.

## Verification

- `make check` — unit: the power-curve math and joules integration across
  sample gaps, coefficient resolution order (annotation > values > table >
  fallback) with fallback loudness, quality threading in the DTO mappers,
  budget estimated-share arithmetic.
- Hub `make test-int` (testcontainers ClickHouse) — seeded **mixed** rows
  (measured + estimated on distinct nodes): per-quality split is exact, totals
  are not blended, node coverage reports the three tiers, budgets count both
  and expose the share.
- `make helm-check` — the estimator container and scrape render **only** when
  `sensor.green.estimation.enabled`; the guard requires `sensor.green.enabled`;
  defaults leave every manifest byte-identical.
- `make e2e` / `make e2e-ui` — seeded estimated series: badge, coverage panel,
  export methodology subsection; disabled-module path unchanged.
- `make e2e-helm` — kind nodes are containers with **no powercap**, so the
  estimator's real activation path (probe fails → estimate) runs in CI
  naturally, alongside the existing Kepler fake-cpu-meter leg; the **TTV and
  probe-canary gates unchanged**.
- **Post-merge**: side-by-side on one RAPL machine — estimator (probe
  overridden) vs Kepler on the same node — to publish the *observed* error band
  the docs will cite instead of the literature figure.

## Roadmap

- [x] AEP accepted
- [x] Re-verify the pinned Kepler line for upstream estimation/VM support
      (may shrink the estimator to config) — confirmed 2026-07-30: Kepler
      v0.11.x (pinned) has no VM/RAPL-less estimation; the old ML-model-server
      approach is documented upstream as legacy/deprecated. Building the
      estimator as designed, no shrinkage.
- [ ] Estimator exporter: RAPL probe, model, cgroup attribution; 5th sensor
      container, opt-in, no probes
- [ ] otel-agent: `measured` stamping on the Kepler scrape + estimator scrape
- [ ] Hub: quality-aware SQL + DTO split + node-coverage read (closes the
      green-carbon follow-up)
- [ ] UI: estimated badge, coverage panel, preflight copy
- [ ] Export: estimation methodology subsection
- [ ] Helm: values + schema + guards + template tests
- [ ] e2e / e2e-helm estimator legs; RAPL side-by-side error-band note
- [ ] Docs (measured vs estimated, coefficients, methodology) via docs-align
- [ ] Post-v1: hypervisor-assisted input (Scaphandre qemu / Kepler VM support),
      curve exponent, per-tenant coefficients
