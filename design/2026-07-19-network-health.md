# AEP: Network health on the service map — per-edge RTT and connection failures

- **Date:** 2026-07-19
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Surface **connection-level health on the service map**: per-edge **TCP RTT**
and **failed/reset connections**, from OBI's TCP-stats (StatsO11y) metrics —
zero-code, no traces, no SDKs. Today the map's flow edges carry only byte
volume (`obi.network.flow.bytes`); this adds the L4 health OBI already measures
between the same endpoints. It is **not a new module**: it extends the
`infra-metrics`-gated flow enrichment and the core service-map handler/UI, the
same seam `NetworkEdges` already uses.

## Motivation

The service map shows call volume (from traces) and trace error rate. It says
nothing about the *network* between services: a link with rising RTT or refused
connections looks fine until the application-level symptoms appear. OBI measures
this in the kernel for every connection — RTT and failed-connection counts —
without touching the app. Putting it on the edge closes the loop: "cart→payments
is slow" becomes "cart→payments RTT p95 is 180ms and 4% of connections are
failing," visible on the topology the operator already looks at.

Ties to the [wedge](../AGENTS.md): kernel L4 health, zero app changes, reusing
the OBI sensor already deployed. Ties to
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
OTLP-native metrics into the existing `otel_metrics_*` tables; the hub reads via
SQL; no proprietary format.

### Spike result (why the scope is what it is)

Verified against OBI v0.9.0's metric reference (the pinned version):

- ✅ **`obi.stat.tcp.rtt`** — histogram (seconds), per-connection with `src.*`/`dst.*`.
- ✅ **`obi.stat.tcp.failed.connections`** — counter, per-connection, labeled by
  `reason` (this is where "resets" appear — as a failure reason).
- ❌ **Retransmissions** — **not emitted by OBI at all.** Dropped from scope; the
  roadmap's third signal doesn't exist upstream.

Both are in OBI's **StatsO11y feature**, *separate* from the `network`
flow-bytes feature we enable today — so this needs a **new OBI collection flag**,
not just query/UI work. Both are standard OTLP histogram/sum, so **no schema
migration**. OBI's `attributes.select.<metric>.include` lets us force
`k8s.src.owner.name`/`k8s.dst.owner.name` onto them — the keys the map joins on
(confirmed available; the end-to-end attribution is confirmed in the e2e-helm
kind stack where OBI actually runs).

### Goals

- Per-edge **RTT p95** (from the histogram) and **failed-connection count** (from
  the counter) over the selected window, joined to service-map edges by k8s owner.
- Surfaced on the cytoscape edges: a hover **tooltip** (calls, bytes, RTT p95,
  failed connections) and an **edge health signal** (styling when RTT is high or
  connections are failing) — edges have neither today.
- Enabled by an OBI StatsO11y collection flag, gated on `infra-metrics` exactly
  like the flow-bytes edges (`sensor.obi.network.enabled` requires it, and the
  read path checks `modules.InfraMetrics`).
- No schema migration; reuse `otel_metrics_histogram` + `otel_metrics_sum`.

### Non-goals (v1)

- **Retransmissions** — not available from OBI (documented upstream gap).
- **Per-reason failure breakdown** in the UI — v1 shows a total failed count; the
  `reason` label is stored and can be surfaced later.
- **Alerting on edge health** — the alerting module could consume this later
  (edge RTT/failure conditions), but v1 only visualizes.
- A **new module** — the data and gating live in infra-metrics; a separate module
  would duplicate them for no gain.
- True byte-rate on edges — unchanged from today's approximation.

## Solution

### Collection (sensor / OBI)

Extend the OBI `network` config block (`sensor-config.yaml`) to enable the
TCP-stats feature and select k8s-owner attributes on the two stats metrics:

```yaml
network:
  enable: true
  allowed_attributes: [k8s.src.owner.name, k8s.src.namespace, k8s.dst.owner.name, k8s.dst.namespace]
# NEW — TCP stats (RTT + failed connections). Exact feature key pinned against
# OBI v0.9.0 during implementation; attributes forced to the k8s owner keys the
# map joins on.
stats:            # or the metrics `features` list — verified in the build
  enable: true
attributes:
  select:
    obi_stat_tcp_rtt: { include: [k8s.src.owner.name, k8s.dst.owner.name] }
    obi_stat_tcp_failed_connections: { include: [k8s.src.owner.name, k8s.dst.owner.name] }
```

Behind a Helm value (`sensor.obi.network.stats.enabled`, default follows
`network.enabled`), guarded by the existing `infra-metrics` requirement.

### Storage (read-only; no migration)

New store method `NetworkEdgeHealth(ServiceQuery) ([]NetworkEdgeHealth, error)`
(or extend `ServiceEdge`), reading the existing metric tables keyed by
`Attributes['k8s.src.owner.name'] / ['k8s.dst.owner.name']`:

- **RTT p95** from `otel_metrics_histogram` where `MetricName='obi.stat.tcp.rtt'`
  — `quantileMerge`/`quantilesBounded` over the histogram buckets per (src,dst),
  or a bounds-weighted estimate (mirrors how the RED quantiles are derived).
- **Failed connections** from `otel_metrics_sum` where
  `MetricName='obi.stat.tcp.failed.connections'` — `sum(Value)` per (src,dst)
  (the same cumulative-counter approximation `NetworkEdges` documents).

Gated on `modules.InfraMetrics` like `NetworkEdges`. `ServiceEdge` gains
`RTTMs float64` and `FailedConnections uint64` (0 when stats are off/absent), or
a parallel struct merged by (src,dst) in the handler.

### API

Extend `serviceEdgeDTO` (`api/dto.go`) with `rttMs float64` (p95, omitempty) and
`failedConnections uint64` (omitempty). The `/api/v1/service-map` handler merges
edge health into the existing trace+flow edges by (source,target), the same
`mergeEdges` join. Route stays core (unconditional); the enrichment is gated on
infra-metrics inside the handler, as today.

### UI

`ui/src/components/service-map/service-map.tsx`: edges today render only width
(calls) + color (errorRate) and have **no tooltip**. Add:
- an **edge tooltip** (calls, bytes, RTT p95, failed connections), and
- an **edge health style** — amber when RTT p95 exceeds a threshold or failed
  connections are present, distinct from the trace error-rate red (or combined
  into a single "unhealthy edge" treatment).

Extend `ui/src/lib/api-types.ts` `ServiceEdge` (which today lacks even
`bytes`/`provenance`) to carry the flow + health fields, and map them onto the
cytoscape edge data.

### Judgment calls (tell me to change any)

1. **RTT p95** as the headline (not p50/p99) — the useful "is this link slow" number.
2. **Edge-health thresholds**: fixed sensible defaults for v1 (e.g. RTT p95 >
   100ms → amber; any failed connections → amber), not configurable yet.
3. **Combine vs separate** the trace-error-rate red and the network-health amber:
   v1 keeps trace errors red and adds a network-health amber, precedence red > amber.

### Alternatives considered

- **A new `network-health` module** — the metrics live in the infra-metrics
  tables and the edges live on the core map; a module would split gating across
  two owners for no benefit. Extend infra-metrics + core.
- **Store RTT as a gauge** — OBI emits a histogram; reusing `otel_metrics_histogram`
  gives real quantiles and needs no new table.
- **Wait for retransmissions** — OBI doesn't emit them; blocking the whole
  feature on one unavailable signal wastes the two that are available.

### Documented v1 limitations

- ~~**The exact OBI stats-feature config key and per-edge attribution are
  unverified against a running OBI**~~ — **resolved 2026-08-23 against the
  pinned OBI v0.9.0 source, and the guess was wrong twice.** `stats: enable:
  true` addressed a field that does not exist on `StatsConfig`: the config
  loader ignores unknown keys, so the switch was silently inert and the two
  metrics were never collected. Metric families are selected by name in
  `metrics.features` (`pkg/export/feature.go` — `stats`, or the granular
  `stats_tcp_rtt` / `stats_tcp_failed_connections`), which is the only switch
  that exists. Worse, the `attributes.select` block was emitted as a *second*
  `attributes:` mapping beside the Kubernetes decorator; OBI loads its YAML with
  `yaml.v3`, which rejects a repeated root key outright, so the sensor could not
  start at all whenever `stats` was on — which is the default with
  `network.enabled`. Both are fixed, and `template-test.sh` now parses the
  rendered document instead of grepping it. The metric names themselves
  (`obi.stat.tcp.rtt`, `obi.stat.tcp.failed.connections`) and the dotted/
  underscored equivalence of `attributes.select` keys were confirmed correct.
  **Per-edge attribution to `k8s.src.owner.name`/`k8s.dst.owner.name` remains
  unconfirmed on a running sensor** — that still needs the kind/eBPF run.
- ~~**No retransmissions** (OBI gap).~~ — **closed in v0.9.** OBI grew
  `stats_tcp_retransmits` / `obi.stat.tcp.retransmits` at v0.12, and the chart's
  pin moved with it. The metric carries the same k8s owner attributes as the
  other two, so it joins the edge on the same key and appears in the same
  tooltip. It also joined the edge-health test: a link can lose packets and
  still measure fast, which is exactly the fault RTT alone hides. The fourth
  member of OBI's `stats` umbrella, `stats_tcp_io`, is deliberately NOT
  rendered — it fires on every send and receive, and the chart now names each
  stats feature individually so the version bump could not switch it on behind
  an install's back.
- **Failed-connection count is a cumulative-counter `sum()` approximation** for
  edge weighting, the same caveat as `NetworkEdges` byte sums; a per-series delta
  rollup is future work if an exact rate is needed.
- Edges only appear for workloads OBI resolves to a k8s owner (same as flow edges).

## Verification

- **Unit** (`hub/internal/storage/...` + `api`): the health query maps histogram
  buckets → p95 and sum → failed count per (src,dst); handler merges health onto
  edges by (source,target); gating leaves health absent when infra-metrics is off.
- **Integration** (testcontainers ClickHouse): synthetic `obi.stat.tcp.rtt`
  histogram + `obi.stat.tcp.failed.connections` sum rows produce the right
  per-edge p95/failed count.
- **e2e-helm (kind — where OBI runs):** enable the stats feature, drive demo
  traffic, and assert the two metrics land in `otel_metrics_*` **with
  `k8s.src.owner.name`/`k8s.dst.owner.name`** — this is the empirical confirmation
  of per-edge attribution folded into the build.
- **Playwright:** a service-map edge tooltip shows RTT p95 + failed connections;
  an unhealthy edge renders the health style.
- **Helm** (`template-test.sh`): the stats config renders only with the flag and
  infra-metrics; disabled → absent.
- **Full gate:** `make check`, `cd ui && npm run build`.

## Roadmap

- [x] AEP accepted
- [x] Sensor: OBI StatsO11y flag + attribute selection (Helm value, infra-metrics-gated)
- [x] Hub: `NetworkEdgeHealth` query (histogram p95 + failed sum) + edge merge
- [x] API: `serviceEdgeDTO` rttMs/failedConnections
- [x] UI: edge tooltip + health styling; extend `ServiceEdge` type
- [x] Storage p95-from-histogram SQL validated against real ClickHouse (integration test)
- [x] OBI stats config key pinned against the v0.9.0 source (2026-08-23) — the
      shipped `stats.enable` key was inert and the duplicate `attributes:` block
      stopped the sensor from starting; both fixed, rendered configs now parsed
      in `template-test.sh`
- [x] **Confirm per-edge attribution (k8s owner keys on the stats metrics) in a
      kind/eBPF env** — v0.9. `deploy/helm/e2e-helm.sh` now installs with
      `sensor.obi.network.enabled=true` and asserts that `obi.network.flow.bytes`
      and `obi.stat.tcp.rtt` land carrying `k8s.src.owner.name` /
      `k8s.dst.owner.name`, non-empty and different from each other — a series
      labelled with one owner or none would pass a row count while attributing
      nothing. Source-confirmed too: `statsKubeAttributes`
      (`pkg/export/attributes/attr_defs.go`) defaults both keys to true on every
      stats metric when kube metadata is on
- [x] Retransmits: `obi.stat.tcp.retransmits` on the edge, once OBI had it (v0.9)
- [x] **The stats feature could never have run in a container** — v0.9. The
      first real-kernel run of this feature, which is what the confirmation
      above amounts to, found OBI exiting on boot with "neither debugfs nor
      tracefs are mounted": the TCP-stats tracepoint needs the kernel tracing
      filesystem, and the sensor pod mounted it only for the profiler. An
      optional metric was taking traces and flows down with it. That is the
      third distinct way this feature has been wrong while looking right on
      paper, and the reason the caveat above was worth keeping open
- [ ] Docs (service-map page, network-health config) via docs-align
- [ ] Later: per-reason failure breakdown, edge-health alerting (feeds the alerting module), `stats_tcp_io` byte counters if a use case justifies the event volume
