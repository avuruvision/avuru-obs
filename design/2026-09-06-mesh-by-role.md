# AEP: The mesh, by role

- **Date:** 2026-09-06
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

[Mesh-facing surfaces](./2026-08-25-mesh-surfaces.md) gave the mesh a screen: RED for
every proxy, and whether the control plane is still pushing config. This AEP makes that
screen legible on the mesh people actually run, by giving each proxy the two facts the
current table withholds — **what kind of proxy it is** and **where it lives** — and then
building the views those two facts unlock.

Nothing here needs new cluster permissions or a new scrape target.

## Motivation

On an ambient install, `GET /api/v1/mesh/proxies` returns rows like this:

| Workload | Rate | Success | p50 | p95 | Carried in | Carried out |
|---|---|---|---|---|---|---|
| global-waypoint.istio-waypoint | 3.3/s | 100.0% | 25ms | 50.00s | 28 | 0 |
| istio-ingressgateway-istio.istio-edge | 0.3/s | 100.0% | 15ms | 3.44s | 0 | 1 |

Three problems, all of them the same problem.

1. **Every proxy looks alike.** A ztunnel and an ingress gateway are different animals
   doing different jobs, and the transport classifier already knows the difference — it
   is how the hop-collapse walk knows to step over three chained proxies in ambient and
   one in the sidecar model. That knowledge is thrown away before it reaches the screen.
   The table cannot be grouped, filtered or reasoned about, because the only dimension
   it has is the name.

2. **"Carried in / Carried out" is not true.** Those columns render `callsIn`/`callsOut`,
   which are **call counts**. The words say bytes. The bytes exist — OBI writes
   `obi.network.flow.bytes` and `NetworkEdges` already aggregates them for the map — and
   nothing reads them here.

3. **The proxy is a dead end.** Clicking a row does nothing, so the screen answers "which
   proxy is unhealthy" and then abandons the question one step before the useful part:
   *what is it carrying, and who is hurt when it fails*. `CollapsedEdges` already records
   which proxies each recovered dependency crossed (`viaTransport`). That is the answer,
   already computed, never shown.

The wedge argument holds: this is all read from telemetry already in ClickHouse. Nothing
below changes what a fresh cluster has to install to get value in five minutes.

### Goals

- A role per proxy — ingress gateway, egress gateway, waypoint, ztunnel, sidecar, control
  plane — derived from what the mesh already tells us about itself.
- A namespace per proxy, resolved the same way every other surface resolves it.
- A table that can be sorted and faceted, like every other table in the product.
- Bytes that are bytes, and connection health, where the data exists to support them.
- A per-proxy view that answers "what does this carry", from the collapse walk.
- A graph of the mesh with its hops **intact**, as the complement to the service map,
  which exists to hide exactly those hops.
- Deeper control-plane health from the scrape that already runs.

### Non-goals

- **Reading Kubernetes or Istio configuration.** Namespace inventory, mTLS mode, waypoint
  bindings and config validation all need cluster reads the hub does not have and this
  AEP does not add. That is a separate proposal with a separate module and a separate
  RBAC argument, and it must reverse a non-goal recorded in
  [mesh-facing surfaces](./2026-08-25-mesh-surfaces.md) rather than quietly ignore it.
- **Data-plane metric scraping** (Envoy, ztunnel). Discussed under Alternatives; deferred
  until the telemetry-only surfaces are on screen and we know what is still missing.
- **Non-Istio control planes.** Unchanged from the previous AEP: the proxy half is
  mesh-agnostic, the control-plane half is Istio-shaped and says so.

## Solution

### A role, from evidence the mesh already writes

`topology.Role` is a two-member enum — service or transport — and must stay that way: it
is what the map draws, and the map has exactly two shapes. The mesh screen needs a finer
answer to a different question, so it gets a separate `topology.MeshRole`, computed only
for workloads already classified as transport.

Two inputs, in the order the codebase already uses for transport evidence:

1. **Name patterns decide.** The built-in transport globs are deliberately narrow and
   already distinguish the roles: `ztunnel`/`ztunnel-*`, `*-waypoint`/`waypoint-*`,
   `istio-ingressgateway*`, `istio-egressgateway*`, `istiod`, `istio-proxy`. Matching is
   whole-name and per-segment, so `global-waypoint.istio-waypoint` resolves on either half.
2. **Labels refine.** `avuru.transport.istio_component` carries `operator.istio.io/component`,
   whose value separates a control plane (`Pilot`) from gateways. Presence of
   `avuru.transport.gateway` marks a Gateway-API-managed proxy.

The values are already in ClickHouse and read only as a boolean today
(`transportEvidenceExpr` tests key *presence*). `ServiceLabels` gains a
`TransportLabels map[string]string`, extracted **by prefix** rather than by a named key
list — the chart owns which labels are collected, and a hub that restated that list would
drift from it. `MeshRole` consults the keys it understands and ignores the rest, so adding
a label to the chart can never break the hub.

A proxy no rule resolves gets no role, and renders blank. An invented default would be a
worse answer than an honest gap — the same argument that keeps a namespace-less service
outside every boundary on the map instead of in a bucket called "default".

### Bytes, and the columns that lied

`callsIn`/`callsOut` keep their names in the DTO — they are correct, and the UI was
wrong — and the columns become "Calls in" / "Calls out".

Bytes arrive as their own fields from `NetworkEdges`, and connection health (RTT p95,
failed connections, retransmits) from `NetworkEdgeHealth`. Both read `otel_metrics_*`, so
both are gated on `infra-metrics`, and both are **pointers**: an install without that
module must get an absent field and a rendered "—", never a zero. This is the rule the
control-plane half already enforces by test, and the reason it exists is that a proxy
reported as carrying 0 bytes is indistinguishable from a proxy that has failed.

### What a proxy carries

The hop-collapse walk already produces, for every recovered `app → app` dependency, the
list of proxies it crossed. Filtering the existing service-map response for edges whose
`viaTransport` names this proxy answers "what does it carry" with **no new endpoint and no
new SQL** — the same discipline that made the service detail page compose existing reads
rather than grow one of its own, so that two screens cannot disagree about the same graph.

### A graph with the hops left in

The service map exists to hide proxies; hiding them is the feature. The mesh graph is its
complement, and it is nearly free: the client-side view rules already expose
`splitInfrastructure(..., show: true)` and `withoutCollapsed()`. Rendering the existing
map component with collapse **off** and infrastructure **shown** draws
ztunnel → waypoint → ztunnel as three hops, which is what it is. Net-new is a stylesheet
branch keyed on `MeshRole`, so the roles are distinguishable at a glance.

### More of the scrape we already pay for

The istiod scrape keeps four series. Widening the keep-list costs one chart value and buys
push latency (`pilot_xds_push_time`), proxies too slow or gone to receive config
(`pilot_xds_send_time`, `pilot_xds_write_timeout`), config churn (`pilot_k8s_cfg_events`),
listener conflicts (`pilot_conflict_*`) and queue backpressure ahead of convergence
(`pilot_proxy_queue_time`).

`values.yaml` is in the embedded-chart set, so this change carries a `make sync-hub-chart`.

### Alternatives considered

- **Put the role on `topology.Role`.** One enum, fewer concepts — and every consumer of
  the map's `role` field would have to learn six values to keep answering a two-value
  question. Rejected: the map's vocabulary is not the mesh screen's vocabulary.
- **Derive the role in the UI from the name.** No backend change at all, and the glob list
  and its per-install overrides live in the hub. Two implementations of one rule that must
  agree, and the operator's `topology` ConfigMap would silently fail to reach one of them.
  Rejected.
- **Scrape ztunnel (`:15020`) now.** It is the highest-value ambient signal we lack — L4
  bytes and connections per workload, and mTLS state — and it fits the existing sensor-side
  DaemonSet scrape precedent exactly. It is also new collection and new install friction
  for a screen we are about to change substantially. Deferred, not rejected: revisit once
  the telemetry-only views are in front of an operator. **Reversed by
  [what the mesh was told, and what it did](./2026-09-08-mesh-declared-vs-observed.md).**
- **A `/api/v1/mesh/proxies/{name}` detail endpoint.** Convenient, and it would let the
  proxy page disagree with the map about the same edges. Rejected in favour of composing.

## Verification

- **Unit:** `MeshRole` resolution — every ambient name, the sidecar name, label evidence
  beating a silent glob, and an unresolvable name yielding no role rather than a guess.
- **Handler:** roles and namespaces appear on the proxy rows; bytes and connection health
  are absent (not zero) with `infra-metrics` off; the routes still 404 with the module off.
- **Storage integration:** `TransportLabels` extraction against real ClickHouse, including
  a proxy whose spans carry conflicting label values collapsing to the dominant one.
- **Playwright:** sorting and both facets round-trip through the URL; a proxy row opens its
  detail view and back; the graph tab renders hops the service map hides.
- **Chart render:** the widened keep-list appears only with the control-plane scrape on;
  the embedded copy stays in sync.

## Roadmap

- [x] AEP accepted
- [x] `MeshRole` + `TransportLabels` through storage and the proxies DTO
- [x] Sortable, faceted proxy table; honest column names
- [x] Bytes + connection health, `infra-metrics` gated, absent when unmeasured
- [x] Per-proxy detail composed from the existing map read
- [x] Mesh graph tab with hops intact
- [x] Widened istiod keep-list + control-plane card
- [ ] Listener conflicts (`pilot_conflict_*`) and queue time — same scrape, not yet asked for
- [ ] Per-role node styling on the mesh graph (waypoint vs ztunnel vs gateway)
- [ ] Configuration inventory and validation — separate AEP, separate module
