# AEP: Inter-zone traffic accounting — bytes by zone pair from kernel flows

- **Date:** 2026-08-18
- **Author(s):** Berny ryders
- **Status:** Accepted — implemented 2026-08-23

## Summary

Account for **cross-zone traffic**: bytes exchanged between availability
zones, per zone pair, from OBI's `network_inter_zone` feature — zero-code,
kernel-measured, and **low-cardinality by design** (bounded by the number of
zone pairs, not workload pairs). Cross-AZ transfer is billed on most clouds
and is a resilience signal on-prem; today avuru-obs cannot say "zone-a ↔
zone-b moved 40 GB this hour". This is **not a new module**: the metric lands
in the `infra-metrics`-owned `otel_metrics_sum` via the normal OTLP path,
exactly like `obi.network.flow.bytes`, and a small store/API/UI surface reads
it. The docs site already badges "Inter-zone traffic accounting (OBI
`network_inter_zone`)" as planned for v0.6 (`status.mdx`); this AEP formalizes
that announced item.

## Motivation

Cloud bills line-item inter-AZ data transfer, and the usual answer to "what is
driving it?" is a VPC flow-log pipeline or a cloud-cost SaaS. The sensor
already watches every connection in the kernel; OBI v0.9.0 (the chart's
pinned `otel/ebpf-instrument:v0.9.0`) ships zone attribution of those flows
upstream. Turning it on gives operators the zone-pair byte matrix from the
telemetry they already collect — no flow logs, no agents, no egress.

The per-edge `network` feature could in principle be joined to node zones, but
its cardinality scales with workload pairs and it costs hostNetwork plus a
per-edge metric stream. The point of this AEP is the **standalone** shape:
zone accounting *without* per-edge flows, for clusters that want the bill
explained but not the full flow topology.

Ties to the [wedge](../AGENTS.md): kernel measurement, zero app changes, the
OBI sensor already deployed. Ties to
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
OTLP-native metrics into the existing `otel_metrics_*` tables; the hub reads
via SQL; no proprietary format.

### Spike result (source-verified against OBI tag v0.9.0)

Verified in the OBI source at tag v0.9.0, not just docs:

- ✅ **The feature exists at the pinned tag**: `"network_inter_zone"` is in the
  `metrics.features` list (`pkg/export/feature.go:49`).
- ✅ **Metric**: OTLP **`obi.network.inter.zone.bytes`** — an Int64Counter in
  bytes. Only flows where **SrcZone != DstZone** are counted; same-zone
  traffic is skipped at the exporter, so the metric is cross-zone by
  construction.
- ✅ **Attributes**: `k8s.cluster.name`, `src.zone`, `dst.zone` **only** —
  low-cardinality by design. Zones come from the node label
  `topology.kubernetes.io/zone`, resolved pod → HostIp → Node via OBI's kube
  metadata store.
- ✅ **Standalone**: `network_inter_zone` activates the flows pipeline by
  itself and emits only the inter-zone metric — the full `network` feature
  (per-edge `obi.network.flow.bytes`) is **not** required. Both can be on
  together.
- ✅ **Prerequisites**: Kubernetes only (experimental upstream); nodes must
  carry the zone label; OBI kube metadata enabled — the chart already renders
  `attributes.kubernetes.enable: true` unconditionally
  (`deploy/helm/avuruobs/templates/sensor-config.yaml`).
- ✅ **RBAC is already sufficient**: the sensor ClusterRole grants
  `get/list/watch` on `nodes` (alongside pods/services and apps
  replicasets/deployments) in
  `deploy/helm/avuruobs/templates/sensor-rbac.yaml` — the node-zone lookup
  needs no new rule.
- ✅ `attributes.select` is **per metric section**, so the chart's existing
  select entries for the TCP-stats metrics do not affect this metric; the
  default zone attributes need no select entry.
- ✅ Upstream carries a k8s multizone integration test at the tag
  (`internal/test/integration/k8s/netolly_multizone/`).
- ❓ **hostNetwork**: the spike did **not** establish whether OBI's flows
  pipeline observes traffic without the host network namespace. The chart
  today sets `hostNetwork: true` only for `sensor.obi.network.enabled`
  (`sensor-daemonset.yaml`); since inter-zone standalone activates the same
  flows pipeline, v1 conservatively inherits that requirement — a
  verification box below either confirms or relaxes it.

### Goals

- **Bytes per zone pair** over the selected window, tenant-scoped, from
  `obi.network.inter.zone.bytes` in the existing `otel_metrics_sum`.
- **Standalone gating**: a chart flag independent of
  `sensor.obi.network.enabled`, so accounting doesn't drag in per-edge flows —
  but gated on `modules.infraMetrics.enabled` like every `otel_metrics_*`
  consumer.
- A small read surface: store method `ZoneTraffic`, endpoint
  `GET /api/v1/network/zones`, and a compact zone-pair panel on the
  Dashboard's capacity band.
- **No schema migration**; no new tables; no new module.
- CI-provable attribution: kind nodes carry no real zones but can be labeled
  synthetically, so the whole path is asserted in `e2e-helm`.

### Non-goals (v1)

- **Pricing / cost-per-GB** — v1 reports bytes by zone pair only. A €/GB
  factor is operator config + presentation (the green module's static-factor
  pattern) and is a named follow-up, not part of this AEP.
- **Per-workload zone attribution** ("which service crossed zones") — that is
  the per-edge `network` feature joined to zones; this AEP is the cheap
  aggregate. Both flags can be on together for operators who want both.
- **Alerting on cross-zone volume** — the alerting module could consume this
  later; v1 only visualizes.
- **A runtime collection-overlay key** — the overlay's closed schema
  (`hub/internal/collection/overlay.go`) has no `network` key today;
  inter-zone mirrors that and stays chart-defined only (see Judgment calls).
- **Non-Kubernetes environments** — upstream the feature is Kubernetes-only
  and experimental.

## Solution

### Collection (sensor / OBI)

New chart value **`sensor.obi.network.interZone.enabled`**, default
**`false`**. It lives inside the `sensor.obi.network` block because it
configures the same OBI flows family, but it is **effective on its own** —
unlike `network.stats`, which only applies when `network.enabled`.

`sensor-config.yaml` rendering (alongside, not inside, the existing
`network:` block, which stays untouched):

```yaml
{{- if .Values.sensor.obi.network.interZone.enabled }}
# Inter-zone traffic accounting (OBI network_inter_zone, source-verified at
# v0.9.0): kernel flows aggregated to (src.zone, dst.zone) byte counters —
# same-zone traffic is skipped at the exporter. Standalone: activates the
# flows pipeline without the per-edge network feature. Exported as
# obi.network.inter.zone.bytes -> otel_metrics_sum. Zone comes from the node
# label topology.kubernetes.io/zone via OBI's kube metadata store (enabled
# above). Default attributes are already the low-cardinality set — no
# attributes.select entry needed.
otel_metrics_export:
  features:
    - network_inter_zone
{{- end }}
```

The **feature name and its standalone semantics are source-verified** at the
tag; the exact config surface (the metrics exporter's `features` list vs the
legacy `network.enable` toggle the chart uses today, and how they merge when
both flags are on) is **pinned during implementation** against v0.9.0 — the
same discipline the network-health AEP used for the stats key, with the
template-test and the e2e-helm assertion as the empirical check. The existing
`network.enabled` rendering is deliberately left as-is: it ships and is
guarded by tests; unifying both onto the features list is a cleanup for later,
not a prerequisite.

Chart guard, same pattern as the `network` guard at the top of
`sensor-config.yaml`:

```yaml
{{- if and .Values.sensor.obi.network.interZone.enabled (not .Values.modules.infraMetrics.enabled) }}
{{- fail "sensor.obi.network.interZone.enabled requires modules.infraMetrics.enabled — inter-zone byte counters are stored in the otel_metrics_* tables owned by the infra-metrics module." }}
{{- end }}
```

**hostNetwork**: `sensor-daemonset.yaml` currently gates
`hostNetwork: true` + `dnsPolicy: ClusterFirstWithHostNet` on
`sensor.obi.network.enabled` alone. The gate becomes
`or .Values.sensor.obi.network.enabled .Values.sensor.obi.network.interZone.enabled`:
the flows pipeline is the same node-level L4 observation, so standalone
inter-zone conservatively inherits the requirement. Whether the flows
pipeline actually needs the host namespace is an open question the spike did
not answer — if the e2e-helm confirmation shows flows are observed without
it, relaxing the gate (for both flags) is a follow-up, and the values.yaml
comment says so.

### Deploy (chart wiring)

- `values.yaml`: `interZone: { enabled: false }` under `sensor.obi.network`,
  with the standalone/hostNetwork/infra-metrics caveats in the comment block.
- `values.schema.json`: add `interZone.enabled` (boolean) under
  `sensor.obi.network.properties`.
- The hub's embedded chart copy (`hub/internal/collection/chart/`) must stay
  in step: run **`make sync-hub-chart`** after any chart edit (the
  `helm-check` target runs it, and `version-set` now stamps the embedded copy
  too).
- The collection overlay's closed schema is **not** extended: the runtime
  control plane cannot toggle `sensor.obi.network` today, and inter-zone
  mirrors that — chart-defined only for v1.

### Storage (read-only; no migration)

The counter lands in the existing **`otel_metrics_sum`** through the normal
sensor → gateway → ClickHouse pipeline — the `NetworkEdges` precedent
(`hub/internal/storage/clickhouse/services.go`). No new tables, no rollup
jobs; metrics retention already covers it.

New store method on `storage.Store` (interface in
`hub/internal/storage/store.go`, next to `NetworkEdges`/`NetworkEdgeHealth`;
fake in `storagetest/fake.go`):

```go
// ZoneTraffic returns bytes exchanged per (src.zone, dst.zone) pair over the
// window, from OBI's inter-zone counters. Same cumulative-counter sum()
// approximation and infra-metrics gating as NetworkEdges.
ZoneTraffic(ctx context.Context, q ServiceQuery) ([]ZoneTraffic, error)
```

Implementation mirrors `NetworkEdges` exactly: `sum(Value)` grouped by
`Attributes['src.zone']`, `Attributes['dst.zone']`, `Tenant`-scoped, ordered
by bytes desc, with the **same documented caveat** — summing a cumulative Sum
counter is an approximation used for presence and relative weighting, not a
true byte-rate; revisit with a per-series delta rollup if exact rates are
ever needed. Empty-zone strings are filtered in SQL (the empty-owner filter
precedent); no self-pair filter is needed because the exporter already skips
same-zone flows.

### API

**`GET /api/v1/network/zones`** — a small dedicated endpoint, registered
inside the existing `active.Enabled(modules.InfraMetrics)` block in
`hub/internal/api/router.go` (with `/api/v1/infra/*`), RoleViewer, taking the
standard time-range/project params and returning zone pairs + bytes:

```json
{ "zones": [ { "srcZone": "eu-west-1a", "dstZone": "eu-west-1b", "bytes": 42815332352 } ] }
```

Not a service-map extension: the map DTO is edge-shaped by workload identity
and the map handler merges by (source, target) — zones are node topology, not
graph elements, and forcing them into that response couples two different
lifecycles for no gain (see Alternatives).

### UI (v1)

A **compact zone-pair table** on the **Dashboard's capacity band**
(`ui/src/components/dashboard/capacity-band.tsx` — Band 3, "Kubernetes
capacity"): src zone → dst zone, bytes over the selected window, top pairs
first. The capacity band is the right home because it already mounts only
when infra-metrics is active, and it is the band about the cluster estate —
which is what zones are. The map screen's side panel was considered and
rejected: its panels anchor on a selected graph element, and a zone pair is
not one (see Alternatives).

Same honesty rule as the rest of the band: **no rows → no card** (the
capacity band's "renders nothing rather than a row of zeroes" precedent) —
no teaching empty state for an opt-in accounting feature in v1. Bytes render
via the existing `formatBytes`.

### Upholds the enterprise seam

Rows carry `Tenant` like all sensor metrics — `ZoneTraffic` leads with it,
so **per-project views come free**. Storage stays behind `storage.Store`;
retention is the existing metrics policy; the endpoint sits behind the same
auth/RBAC middleware as every read surface.

### Judgment calls (tell me to change any)

1. **Independent flag, not nested activation** — `interZone.enabled` works
   without `network.enabled`. The standalone capability is the point:
   zone accounting at zone-pair cardinality without buying per-edge flows.
2. **Conservative hostNetwork inheritance** — standalone inter-zone flips
   hostNetwork on, same as `network`, until the e2e-helm run proves it
   unnecessary. Honest cost: the flag is not "free" in v1; the values comment
   says so.
3. **Chart-defined only** (no overlay key) — mirrors how the runtime control
   plane treats `network` today; widening the closed overlay schema is a
   deliberate act for a later AEP, not a side effect of this one.
4. **Dashboard placement over the map side panel** — zones are estate, not
   topology elements; the capacity band already carries the infra-metrics
   gate and the glance-then-link idiom.
5. **No cost config in v1** — bytes are the measurement; €/GB is config +
   presentation and arrives with the follow-up, so the measured number ships
   without waiting on a pricing model.

### Alternatives considered

- **Require `sensor.obi.network.enabled`** (nest under it) — rejected: it
  taxes the cheap aggregate with the expensive per-edge stream. The
  low-cardinality standalone mode is the reason this feature is worth having.
- **Extend `/api/v1/service-map`** — rejected: zones aren't map elements; the
  handler's (source, target) merge and the map's query lifecycle don't fit a
  zone matrix. A dedicated endpoint is ~40 lines and keeps both shapes clean.
- **Derive zones hub-side** by joining per-edge flows to node zones —
  rejected: requires the full `network` feature (defeats standalone), adds a
  join against node inventory, and duplicates attribution OBI already does at
  the source with node-label truth.
- **A new module** — rejected, same reasoning as network health: the data
  lives in infra-metrics-owned tables; a module would split gating across two
  owners for no benefit.
- **Overlay key for runtime toggling** — rejected for v1: the overlay is a
  closed schema and `network` isn't in it either; mirror first, widen later
  if operators actually toggle this at runtime.

### Documented v1 limitations

- **Cumulative-counter `sum()` approximation** — same caveat as
  `NetworkEdges`: relative weighting, not an exact byte-rate.
- **hostNetwork is inherited, not proven necessary** — standalone inter-zone
  costs the host network namespace in v1 until the kind run answers whether
  the flows pipeline needs it (verification box).
- **Exact OBI config rendering pinned in implementation** — the feature name
  and semantics are source-verified at v0.9.0; the features-list/legacy
  `network.enable` interaction is confirmed by template-test + e2e-helm, not
  assumed.
- **Partial labeling undercounts** — confirmed on the cluster: an endpoint the
  sensor cannot resolve to a labelled node yields a row with an empty zone
  attribute (not a skipped flow), and those rows can outweigh the real
  crossings. The query drops them, so an under-labelled cluster reports LESS
  cross-zone traffic than it moves — never a pair with a blank half, which
  would read as a real zone named "".
- **Experimental upstream, Kubernetes-only** — the feature is marked
  experimental in OBI; the values comment says so.

## Verification

- **Unit** (`hub/internal/storage/...` + `api`): `ZoneTraffic` scan/ordering;
  router registers `/api/v1/network/zones` only with infra-metrics; DTO shape.
- **Integration** (testcontainers ClickHouse): seeded
  `obi.network.inter.zone.bytes` rows produce correct per-pair sums; empty
  zones filtered; tenant isolation (the `NetworkEdges` test pattern).
- **e2e-helm (kind — where OBI runs):** kind nodes carry no real zones but
  CAN be labeled synthetically. The script today creates a single-node
  cluster (`kind create cluster`, no config — `deploy/helm/e2e-helm.sh`), so
  this leg adds a two-node kind config, labels the two nodes with different
  `topology.kubernetes.io/zone` values, enables
  `sensor.obi.network.interZone.enabled` on the same install (the green
  opt-in pattern), drives demo traffic across nodes, and **asserts rows in
  `otel_metrics_sum` with `MetricName='obi.network.inter.zone.bytes'` and
  differing `src.zone`/`dst.zone` attributes** — end-to-end attribution in
  CI. Node bring-up happens before T0, so the TTV wedge gate and the
  probe-canary gate run unchanged.
- **Playwright:** the dashboard zone panel renders from seed; absent when no
  rows / module off.
- **Helm** (`template-test.sh` + `make helm-check`): the features entry
  renders only with the flag; the `{{ fail }}` guard fires without
  infra-metrics; hostNetwork is set with interZone on and network off;
  everything disappears when disabled; `sync-hub-chart` leaves no diff.
- **Full gate:** `make check`, `cd ui && npm run build`.

## What the cluster said

The first real run produced four zone pairs:

```
        zone-b   251859958
zone-b           2391119
zone-a  zone-b   1375240
zone-b  zone-a   171510
```

Two are crossings. **The other two carry an empty zone on one side** — traffic
whose peer the sensor could not resolve to a labelled node — and they are by far
the largest, which means an unfiltered query would have been dominated by rows
that name no zone at all. The AEP guessed at this ("what OBI emits for an
unlabeled node… confirmed in the kind run either way") and specified the blank
filter defensively; it turns out to be load-bearing. `ZoneTraffic` drops them in
SQL, and the limitation below is now a measured fact rather than a caveat.

## What implementation changed

**The config surface was pinned, and pinning it uncovered that the block this
feature renders into was broken.** The AEP planned to write the feature into
`otel_metrics_export.features`; the key OBI v0.9.0 actually reads is
`metrics.features`, and naming any feature there *replaces* the default list
rather than adding to it — so `application` has to be repeated or turning flows
on silently turns OBI's application metrics off.

Getting there meant reading the neighbouring lines, which turned out to be
wrong in three places and are fixed in their own change: a duplicated
`attributes:` root key that stopped the sensor from starting at all, a
`stats.enable` key that does not exist, and a `network.allowed_attributes` key
that does not exist either — so the cardinality bound the chart documented was
never applied. See the network-health AEP's limitations section.

`network.enable: true` is kept beside the feature list for the per-edge case:
redundant and deprecated upstream, but it is the switch that has actually been
exercised.

## Roadmap

- [x] AEP accepted
- [x] Sensor: `sensor.obi.network.interZone.enabled` value + schema +
      features-list rendering + infra-metrics `{{ fail }}` guard +
      hostNetwork gate widened to `or(network, interZone)`; `make sync-hub-chart`
- [x] Hub: `ZoneTraffic` store method (+ fake) with the documented
      cumulative-counter caveat; ClickHouse integration test incl. tenant
      isolation
- [x] API: `GET /api/v1/network/zones` inside the infra-metrics route block
- [x] UI: zone-pair table on the Dashboard capacity band (no-rows → no card)
- [x] **e2e-helm: two-node kind config + synthetic
      `topology.kubernetes.io/zone` labels + flag on; assert
      `obi.network.inter.zone.bytes` rows with differing src/dst zones in
      `otel_metrics_sum`** — passed on the first real run, and every existing
      gate (TTV wedge, probe canary, do-no-harm soak, wider ingest, dual-write,
      green, TDP, collection runtime control) passed unchanged beside it
- [x] Confirmed in that run: the existing `nodes` list/watch grant is
      sufficient (no new RBAC rule), and **the sensor does emit rows with an
      EMPTY zone on one side** — see below
- [ ] Whether the flows pipeline works without hostNetwork (would relax the
      gate for both flags) — still unproven; the run had it on
- [ ] Docs (feature page, config reference, v0.6 status flip) via docs-align
- [ ] Follow-ups: cost-per-GB factors (green static-factor pattern),
      per-workload zone attribution (join with the `network` feature),
      cross-zone volume alerting, overlay key if runtime toggling is wanted
