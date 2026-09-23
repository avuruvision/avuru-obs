# Green TDP estimation — implementation design

**Date:** 2026-07-30
**Status:** Approved for implementation
**Author:** brainstorming session
**Target:** `main` (0.3.0-SNAPSHOT) → **v0.3** (the only new feature this release)
**Base AEP:** [design/2026-07-28-green-tdp-estimation.md](../../design/2026-07-28-green-tdp-estimation.md) (Accepted) — this document is the concrete engineering plan on top of that AEP's architecture; it does not re-litigate the AEP's decisions, only fills in the parts left open there.

## 1. Context

The [green module](../../design/2026-07-22-green-carbon.md) (merged, v0.2.0) reports energy/carbon only where Kepler measures RAPL — most virtualized nodes expose none, and `/green` shows an empty teaching state there. This AEP-plus-plan adds an opt-in TDP-based estimator: a new first-party exporter in the sensor DaemonSet that activates only on RAPL-less nodes, emits the same Kepler metric names, and is stamped `avuruops_quality=estimated` end-to-end (SQL → API → UI → CSRD export), never blended with measured numbers.

This is the **only** new feature targeted for v0.3 — once it lands on main with green CI, v0.3 is cut using the same recipe as v0.2.0.

## 2. Decisions made in this session (not yet in the base AEP)

1. **Kepler re-check — done, confirmed still needed.** The pinned Kepler line (`v0.11.4`, [values.yaml:360](../../deploy/helm/avuruops/values.yaml#L360)) has no VM/RAPL-less estimation support upstream; the old ML-model-server approach is documented as legacy/deprecated. Building the custom estimator as designed — no shrinkage to config. (AEP roadmap item checked off.)
2. **Coefficient table: comprehensive, sourced primarily from Cloud Carbon Footprint (Apache-2.0)**, not Boavizta (ODbL — share-alike for the *database*, corrected from the AEP's original "permissively licensed" claim). Boavizta facts are used only individually-cited, to fill specific CCF coverage gaps, never bulk-copied. See §6.
3. **New component is a new first-party Go module** at `sensor/tdp-estimator/`, following the existing per-component-module convention (`gateway/sentryreceiver/`, `tools/seed/`) — own `go.mod`, own `Dockerfile`, own container image, **new entry in the release image matrix** (today only `hub`/`ui`/`gateway` are built — this is the first first-party sensor-pod image).
4. **Real-RAPL side-by-side validation stays post-merge, non-blocking** — same posture as v0.2.0, which shipped green/network-health with hardware validation still open. `kind` nodes have no powercap, so CI naturally exercises the "probe fails → estimate" path without real hardware.
5. **Release-cut sequencing**: implement → merge to main → `make check`/`make test-int`/`make e2e`/`make helm-check` green → docs-align → cut `v0.3` (bump `VERSION`, tag, `v0.3` branch, main → `0.4.0-SNAPSHOT`, `release.yml` runs) — SSH-signing and GHCR-visibility calls stay user-gated as before.

## 3. Architecture (recap, unchanged from the AEP)

```
RAPL present ── Kepler (4th container, unchanged) ──▶ kepler_* series ── avuruops_quality="measured"
RAPL absent  ── tdp-estimator (5th container, opt-in, dormant if RAPL) ──▶ same kepler_* names, avuruops_quality="estimated"
        │
  [same otel-agent prometheus receiver, same keep-regex kepler_(node|pod)_cpu_(watts|joules_total)]
        │ OTLP
        ▼
  gateway ──▶ otel_metrics_sum / otel_metrics_gauge (existing tables, no migration)
        │
  hub green module: quality-aware Wh + gCO2e, node coverage (measured / estimated / absent)
```

No new ClickHouse tables. No new module (`sensor.green.estimation` is a sub-flag of the already-existing `green` module, exactly like `sensor.green.fakeCpuMeter`).

## 4. Component: `sensor/tdp-estimator/`

### 4.1 Layout

```
sensor/tdp-estimator/
  go.mod                  // module github.com/avuru/avuru-obs/sensor/tdp-estimator
  main.go                 // flags, RAPL probe, sampler loop, /metrics HTTP server
  rapl.go                 // RAPL probe: readable /sys/class/powercap/intel-rapl* ?
  sampler.go              // /proc/stat node utilization; cgroup v2 cpu.stat per-pod
  model.go                // P = P_idle + u*(P_max-P_idle); joule integration
  coefficients.go         // resolution order: annotation > values > table > fallback
  coefficients_table.go   // bundled CCF/Boavizta table (see §6)
  metrics.go              // Prometheus exposition: kepler_{node,pod}_cpu_{watts,joules_total}
  *_test.go               // table-driven unit tests per file above
  Dockerfile
```

### 4.2 RAPL probe

At startup, check `/sys/class/powercap/intel-rapl*` is readable. If so: serve an empty `/metrics`, log why, exit the sampler loop (stay alive, no crash-loop — matches Kepler's do-no-harm posture). If not: proceed to sampling. A node's power interface doesn't change under a running pod, so no re-probe loop is needed — a reboot restarts the DaemonSet pod.

### 4.3 Sampling & model

- Node utilization: `/proc/stat` aggregate (all cores), sampled on a fixed interval aligned with the otel-agent scrape (`sensor.green.estimation.scrapeInterval`, default matching Kepler's).
- Per-pod CPU: cgroup v2 `cpu.stat` (`usage_usec`), same accounting kubelet already reads — **cgroup v2 required**, undetected cgroup v1 is a documented unsupported-fleet limitation (AEP §Documented limitations).
- `P_node(t) = P_idle + u(t) × (P_max − P_idle)`. Node joules = integral over samples (trapezoidal, sample-gap tolerant — unit-tested against gapped sample sequences).
- Per-pod joules split only the **dynamic** part (`P − P_idle`) proportional to pod CPU-share of the node; idle power stays node-only, landing in the hub's existing unattributed bucket — never attributed to a pod (AEP non-goal: no dishonest attribution).

### 4.4 Coefficient resolution (Go signature)

```go
// coefficients.go
type Coefficients struct {
    IdleWatts, MaxWatts float64
    Tier                string // "annotation" | "values" | "table" | "fallback"
    Provenance          string // human-readable citation, threaded to export methodology
}

func Resolve(nodeAnnotations map[string]string, valuesIdle, valuesMax float64, cpuModel string) Coefficients
```

Order: node annotation (`obs.avuru.io/power-idle-watts`/`-max-watts`) → Helm values → bundled table lookup by `/proc/cpuinfo` model string → generic per-core fallback (loud: logs a warning, methodology carries a "wide error band" note). `Tier`/`Provenance` ride into the metric's resource attributes so the hub (and the export) can cite per-node provenance without a separate side-channel.

### 4.5 Metrics endpoint

Exact metric names/labels from the [green-carbon AEP's table](../../design/2026-07-22-green-carbon.md#metric-names-single-source-of-truth): `kepler_node_cpu_watts`, `kepler_node_cpu_joules_total`, `kepler_pod_cpu_watts`, `kepler_pod_cpu_joules_total`, with `pod_name`/`pod_namespace` labels on the pod series — bound to `127.0.0.1:{{ .Values.sensor.green.estimation.port }}` (loopback only, same convention as Kepler's port 28282; a distinct port, e.g. `28283`). **No liveness/readiness/startup probes** on the container.

### 4.6 Build & release

New `sensor/tdp-estimator/Dockerfile` mirrors [hub/Dockerfile](../../hub/Dockerfile) (`golang:1.26-alpine` build stage → `gcr.io/distroless/static-debian12:nonroot` runtime, `CGO_ENABLED=0`). New entry in [.github/workflows/release.yml](../../.github/workflows/release.yml)'s `images` matrix:

```yaml
- component: tdp-estimator
  dockerfile: sensor/tdp-estimator/Dockerfile
```

Same multi-arch (`linux/amd64,linux/arm64` — arm64 is not optional, sensor images run on arm64 nodes), cosign signing, SBOM/provenance as the other three images.

## 5. Helm chart changes

### 5.1 Values (`deploy/helm/avuruops/values.yaml`, extending the existing `sensor.green` block)

```yaml
sensor:
  green:
    enabled: false
    estimation:
      enabled: false          # requires sensor.green.enabled
      image:
        repository: <harbor/ghcr path>/avuru-obs-tdp-estimator
        tag: "<pinned to release tag, like the other first-party images>"
      port: 28283
      scrapeInterval: 30s
      idleWatts: 0             # 0 = defer to bundled table / annotation
      maxWatts: 0
```

### 5.2 New chart guard helper (`_helpers.tpl`, alongside `avuruops.collectGreen`)

```gotemplate
{{- define "avuruops.collectGreenEstimation" -}}
{{- if and (include "avuruops.collectGreen" .) .Values.sensor.green.estimation.enabled -}}true{{- end -}}
{{- end -}}
```

Fails the same way the existing guards do if `sensor.green.estimation.enabled` is set without `sensor.green.enabled` (chart-level validation in `sensor-config.yaml`'s guard block, alongside the existing `modules.green`/`modules.infraMetrics` and `sensor.green`/`sensor.agent` guards).

### 5.3 New container (`sensor-daemonset.yaml`, 5th container, after Kepler at line ~294)

Mirrors the Kepler container block exactly in spirit: gated on `avuruops.collectGreenEstimation`, **no probes**, host `/proc` and `/sys/fs/cgroup` mounted read-only, `NODE_NAME` env for labeling.

### 5.4 otel-agent scrape + quality stamping (`sensor-config.yaml`)

Extend the existing `prometheus/green` receiver with a second scrape job (guarded by `avuruops.collectGreenEstimation`):

```yaml
prometheus/green:
  config:
    scrape_configs:
      - job_name: kepler
        # ... unchanged
      {{- if include "avuruops.collectGreenEstimation" . }}
      - job_name: tdp-estimator
        scrape_interval: {{ .Values.sensor.green.estimation.scrapeInterval }}
        static_configs:
          - targets: ["127.0.0.1:{{ .Values.sensor.green.estimation.port }}"]
        metric_relabel_configs:
          - source_labels: [__name__]
            regex: {{ .Values.sensor.green.metrics.keep | quote }}
            action: keep
      {{- end }}
```

New processor step `transform/green_quality` in the `metrics/green` pipeline (after `transform/green`, before `groupbyattrs/green`), stamping quality from the Prometheus receiver's `service.name` resource attribute (the upstream `prometheusreceiver` sets this from `job_name` — **verify this against the pinned otel-agent image's actual behavior in phase 2**; if it differs, fall back to a `job` metric_relabel_configs label instead, same effect):

```yaml
transform/green_quality:
  error_mode: ignore
  metric_statements:
    - context: datapoint
      statements:
        - set(attributes["avuruops_quality"], "measured") where resource.attributes["service.name"] == "kepler"
        - set(attributes["avuruops_quality"], "estimated") where resource.attributes["service.name"] == "tdp-estimator"
```

This runs unconditionally inside the existing `{{- if include "avuruops.collectGreen" . }}` block (both jobs may or may not be active; the statements are no-ops when their job isn't scraping).

### 5.5 Chart tests

Extend [template-test.sh](../../deploy/helm/template-test.sh)'s green section (currently lines 208-302): opt-off (estimation disabled) renders nothing new; opt-on renders the 5th container with no probes, the second scrape job, the quality-stamping transform; the guard (`estimation.enabled` without `sensor.green.enabled`) fails chart validation; default renders are byte-identical to before this change.

## 6. Coefficient table (`sensor/tdp-estimator/coefficients_table.go`)

Same hand-authored-Go-map style as [hub/internal/green/intensity.go](../../hub/internal/green/intensity.go) (the grid-intensity precedent this AEP explicitly follows): a `const` dataset-name+vintage identifier, a map literal, per-entry provenance comments.

```go
// Provenance: primarily Cloud Carbon Footprint's SPECpower-derived coefficient
// data (Apache-2.0, github.com/cloud-carbon-footprint/cloud-carbon-coefficients),
// covering common AWS/GCP/Azure instance-family CPU generations (Intel Xeon
// Scalable gens, AMD EPYC gens, AWS Graviton). A bounded number of entries are
// individually cited from Boavizta's environmental-footprint-data (ODbL) only
// where CCF has no coverage — specific facts, not a bulk copy of their database.
const coefficientDataset = "CCF 2026 + Boavizta (cited gaps)"

var cpuCoefficients = map[string]Coefficients{
    "Intel(R) Xeon(R) Platinum 8259CL": {IdleWatts: 0.74, MaxWatts: 3.5, Tier: "table"}, // CCF, AWS c5
    // ... comprehensive entries across common cloud + bare-metal CPU models
}
```

Matching is by normalized `/proc/cpuinfo` `model name` substring, same spirit as Kepler's own CPU model detection. Vintage and full source list are quoted verbatim in the CSRD export methodology (§8).

## 7. Hub changes

### 7.1 Quality as a grouping dimension (`hub/internal/storage/clickhouse/green.go`)

`avuruops_quality` rides the existing `Attributes` map on every green datapoint — no migration. Both `ServiceEnergy` and `NodeEnergy` SQL gain `Attributes['avuruops_quality'] AS quality` in the `SELECT`/`GROUP BY` (added to `series_deltas`'s grouping, **not** to `greenSeriesID` — quality is a label on an already-uniquely-identified series, not part of series identity, so the delta math is unaffected). Output rows are now `(service, quality, t, wh)` / `(node, quality, t, wh)`.

```go
// storage.ServiceEnergy / NodeEnergy gain a Quality field alongside Points:
type ServiceEnergy struct {
    Service   string
    Quality   string // "measured" | "estimated" | "" (mixed/legacy series, pre-AEP)
    WattHours float64
    Points    []EnergyPoint
}
```

`storagetest.Fake` (`hub/internal/storage/storagetest/fake.go:40-41,232-247`) gets the `Quality` field threaded through in lockstep.

### 7.2 Node coverage (new read, closes the green-carbon follow-up)

New `storage.Store` method:

```go
// NodeCoverage reports, per node, whether it contributed measured, estimated,
// or no green energy in the window. "Known nodes" is the ListAgentNodes
// universe (self-telemetry presence), cross-referenced against NodeEnergy's
// quality-grouped rows — a node present in ListAgentNodes but absent from both
// energy queries is the "absent" tier the green-carbon review asked to make visible.
NodeCoverage(ctx context.Context, q GreenQuery) (NodeCoverage, error)

type NodeCoverage struct {
    KnownNodes    int
    MeasuredNodes int
    EstimatedNodes int
    AbsentNodes   int
}
```

### 7.3 API DTOs (`hub/internal/api/green.go`)

`greenTotalsDTO` splits into measured/estimated (never silently blended):

```go
type greenTotalsDTO struct {
    AttributedWh    float64 `json:"attributedWh"`
    MeasuredWh      float64 `json:"measuredWh"`
    EstimatedWh     float64 `json:"estimatedWh"`
    UnattributedWh  float64 `json:"unattributedWh"`
    Coverage        float64 `json:"coverage"`
    GCO2e           float64 `json:"gco2e"`
    NodeCoverage    nodeCoverageDTO `json:"nodeCoverage"`
}

type nodeCoverageDTO struct {
    Known, Measured, Estimated, Absent int
}
```

`greenServiceDTO` gains `EstimatedWh`/`EstimatedShare` fields (0 when the service's energy is entirely measured — omitted from JSON via `omitempty` like the existing `Requests` field). `buildGreenRows` (`green.go:211-249`) is extended to fold quality into its per-service accumulation before the existing topN logic runs (no change to topN/unattributed-row semantics).

### 7.4 Budgets (`hub/internal/api/green_budgets.go`)

Budget evaluation includes estimated energy in the usage total (an all-VM fleet must be able to trip a budget — AEP explicit goal), but the budget-status DTO carries an `EstimatedShare` field so a threshold crossed mostly on modeled energy is visibly soft in the UI and any alert payload.

## 8. UI changes

- **Badge** (`ui/src/components/green/`): new `quality-badge.tsx` following the [`badge.tsx`](../../ui/src/components/ui/badge.tsx) `cva` tone-variant pattern and the [`severity-badge.tsx`](../../ui/src/components/logs/severity-badge.tsx) lookup-wrapper precedent — `measured` → neutral/success tone, `estimated` → warning tone with a tooltip citing the ±30-50% error band.
- **Coverage panel**: new component in `green-screen.tsx`'s orchestration, rendering the three `nodeCoverageDTO` tiers (known/measured/estimated/absent) as a stacked indicator, next to the existing stat tiles.
- **Preflight copy** (`green-empty-state.tsx`): when estimation is enabled and reporting, flip from "no RAPL, no data" to "estimating via TDP model (±30–50%)" with a link to the methodology.
- **Wire types** (`ui/src/lib/api-types.ts:506-569`): mirror the new `measuredWh`/`estimatedWh`/`nodeCoverage`/`estimatedShare` fields in `GreenTotals`/`GreenServiceEnergy`/`GreenBudget`.

## 9. CSRD export changes (`hub/internal/api/green_report.go`)

`greenMethodologyDTO` gains an estimation subsection:

```go
type greenEstimationDTO struct {
    FormulaLiteral   string   `json:"formula"` // "P = P_idle + u × (P_max - P_idle)"
    CoefficientDataset string `json:"coefficientDataset"` // vintage string from §6
    ErrorBand        string   `json:"errorBand"`  // "±30-50% typical absolute error"
    NodeProvenance   []nodeProvenanceDTO `json:"nodeProvenance"` // per-node tier + citation
}
```

`buildMethodology` (`green_report.go:93-115`) populates this only when the window contains any estimated energy (omitted entirely on a fully-measured or fully-empty report — the export stays boring/unchanged for installs that never enable estimation). `writeGreenCSV` gains the corresponding `# ` metadata lines, same pattern as the existing methodology block.

## 10. Testing plan

| Layer | File(s) | What |
|---|---|---|
| Estimator unit | `sensor/tdp-estimator/{model,coefficients,sampler}_test.go` | power-curve math across sample gaps; resolution order + fallback loudness; cgroup parsing |
| Hub unit | `hub/internal/api/green_test.go` (extend) | quality threading in DTO mappers, budget estimated-share arithmetic |
| Hub integration | `hub/internal/storage/clickhouse/green_integration_test.go` (extend) | seeded mixed measured+estimated rows: per-quality split exact, totals never blended, node coverage reports three tiers |
| Helm | `deploy/helm/template-test.sh` (extend) | estimator container/scrape/transform render only when `estimation.enabled`; guard fails correctly; defaults byte-identical |
| e2e | `e2e/green_test.go` (extend) | seeded estimated series: badge, coverage panel, export methodology subsection |
| e2e-helm | `deploy/helm/e2e-helm.sh` (existing gate, extend) | kind nodes have no powercap — the real "probe fails → estimate" path runs in CI alongside Kepler's fake-cpu-meter leg; TTV + probe-canary gates unchanged |
| UI e2e | `ui/e2e/green.spec.ts` (extend) | badge, coverage panel, preflight copy flip |
| Post-merge (non-blocking) | manual | side-by-side on one real RAPL machine (estimator probe overridden vs Kepler) — publishes the *observed* error band the docs will cite instead of the literature figure |

## 11. Suggested implementation phasing (for writing-plans)

1. **Estimator core** — `sensor/tdp-estimator/` module: RAPL probe, sampler, model, coefficient resolution + table, metrics endpoint, unit tests, Dockerfile. Independently testable with no Helm/hub changes.
2. **Helm wiring** — values, guard helper, container, scrape job, quality-stamping transform, chart tests. Depends on (1)'s image existing (can use a dev tag until release).
3. **Hub quality-aware reads** — SQL/DTO/storagetest changes, node-coverage read, budget estimated-share. Depends on nothing from (1)/(2) — can proceed in parallel once the `avuruops_quality` attribute shape is agreed (§7.1).
4. **UI + export** — badge, coverage panel, preflight copy, methodology subsection. Depends on (3)'s DTO shapes.
5. **e2e / e2e-helm / docs-align** — full-stack verification, then the `docs-align` skill for the bilingual docs site.
6. **Release cut** — `RELEASING.md`/`RELEASE-CHECKLIST.md` process for v0.3, once (1)-(5) are green on main.

Phases 1 and 3 have no dependency on each other and can run concurrently; 2 depends on 1; 4 depends on 3; 5 depends on 2+4; 6 depends on 5.
