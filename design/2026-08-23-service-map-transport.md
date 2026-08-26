# AEP: Transport workloads on the service map

- **Date:** 2026-08-23
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

The service map draws edges between services that never call each other. On a
cluster running a service mesh, every application call is intercepted by a
proxy, so what the map receives is `app → proxy → app`: two edges, neither of
them a dependency. This AEP adds a single classification — is this workload an
application, or is it *transport* that carries other services' traffic — and
hides transport from the map by default. It also stops the map counting
flow-derived edges as "call edges", which they are not.

## Motivation

An install reported a map showing eleven services and two edges, where both
edges ran into mesh components (`global-waypoint.istio-waypoint`,
`istio-ingressgateway-istio.istio-edge`) rather than between the applications
that were actually talking. The map was not wrong about the bytes; it was wrong
about the *claim*. A dependency graph that asserts relationships which do not
exist is worse than one that omits them, because the reader cannot tell which
edges to trust.

Two mechanisms produce this, and both are in shipped code:

1. **Flow edges over-connect.** `NetworkEdges`
   (`hub/internal/storage/clickhouse/services.go`) promotes any
   `k8s.src.owner.name → k8s.dst.owner.name` byte flow into an edge. At the
   kernel a meshed call *is* `app → proxy → app`, so the query faithfully
   reports the transport hops.
2. **Proxies emit spans.** A mesh proxy and an ingress gateway export their own
   Server/Client spans, so `ServiceEdges` — whose parent/child join is sound —
   legitimately produces `app → waypoint` and `waypoint → app`. The join is
   right; the rendering treats a hop as a dependency.

Separately, the map's own count line called every edge a "call edge" while
flow-derived edges carry `Count = 0` by construction.

This is a wedge problem, not a polish problem: "fresh cluster → live service
map in under 5 minutes" is the promise, and a mesh is exactly what a
production-shaped cluster has. The map being immediately wrong on such a
cluster costs the first five minutes.

### Goals

- The default map shows application dependencies and only those.
- The classification is correctable per install, without waiting for a release.
- Flow-derived edges are visibly and numerically distinct from traced calls.
- No change to what the hub stores or queries.

### Non-goals

- **Collapsing the hop.** Reconstructing `app → app` from `app → proxy → app`
  needs per-trace ancestry, and the naive version — pairing every inbound edge
  of a proxy with every outbound one — invents an N×M cross-product of edges
  that are as fictional as the ones being removed. Deferred; see Roadmap.
- Mesh observability as a feature (sidecar load, control-plane health).
- Changing `NetworkEdges` or `ServiceEdges` SQL.

## Solution

A new pure-logic package, `hub/internal/topology`, owns one question: given a
workload name, is it `service` or `transport`? It matches glob patterns
case-insensitively against the full name **and** each dot-separated segment,
because the same workload arrives as a bare service name from OTLP resource
attributes and as `workload.namespace` from OBI's flow labels.

The built-in pattern list is deliberately narrow. A false positive erases a
real service from the map, which is a worse failure than the noise being
removed, so nothing generic (`*-gateway`, `*-proxy`, `istio-*`) is in it — only
exact upstream component names and prefixes distinctive enough that an
application would have to be trying to collide.

Configuration is a mounted JSON file (`AVURUOBS_TOPOLOGY_CONFIG`), hot-reloaded
like the groups/alerts/green configs, with three knobs: `transport` adds
patterns, `applications` forces `service` and wins over everything, and
`disableDefaults` drops the built-ins entirely. It is rendered by the chart on
**every** hub rather than behind a module toggle, because the service map is
core and cannot be turned off — so the knob that corrects it must always exist.

`GET /api/v1/service-map` stamps `role: "transport"` on classified nodes and
omits the field otherwise, so an install with no mesh returns byte-identical
JSON. **The hub reports the classification; it does not act on it.** Dropping
the rows server-side would make the mesh unobservable and would bake a view
decision into the API.

The UI hides transport nodes — and every edge touching one, since that edge
*is* the hop — behind a `?infra=true` toggle in the map toolbar, and shows the
hidden count in the line under it. The dashboard's overview map applies the
same split with no toggle. Two rendering corrections ride along: a flow-only
edge is drawn dotted (dashed already means network-unhealthy) and described as
"network flow · no traced calls" instead of "0 rpm", and the count line
separates call edges from network flows.

The map's six load-bearing visual channels (ring = health, fill = identity,
size = rate, halo = carbon, width = calls, line colour = network/errors) are
untouched: transport uses *shape*, and flow uses *line style*.

### Alternatives considered

- **Filter server-side.** Consistent with `includeAux`, but that parameter is
  a SQL predicate that changes the aggregates; this is a whole-node view
  decision that changes none. Client-side means the toggle costs no refetch and
  the API stays complete.
- **Mark transport but keep it visible.** Honest, but leaves the wrong edges on
  screen — which is the actual complaint.
- **Detect the mesh from Kubernetes labels** (`service.istio.io/canonical-name`,
  `linkerd.io/control-plane-ns`). Better signal than names, and the right
  long-term answer, but it needs attributes the pipeline does not carry today
  and would not have fixed the reported install.
- **Do nothing until the hop can be collapsed properly.** Leaves a map making
  false claims for at least a release.

## Verification

- `hub/internal/topology` unit tests, table-driven, including the exact
  workload names from the reported install and a block of real service names
  (`payment-gateway`, `api-gateway`, `apisix-gateway`, `envoy-fleet-manager`)
  that MUST stay classified as applications.
- Handler tests: the role is stamped, config overrides reach through, and an
  application's JSON carries no `role` key at all.
- `hub/cmd/hub` loader tests: unset env → built-ins; a bad file fails the boot.
- Chart render assertions: env, ConfigMap and an isolated mount dir on every
  hub; operator patterns reach the ConfigMap; nothing renders on a hub-less
  install.
- Playwright (`ui/e2e/service-map-mesh.spec.ts`), against a stubbed map payload
  shaped like the reported cluster: hops hidden by default, the toggle restores
  them and round-trips through the URL, and no toggle appears without a mesh.

**The risk this must not realise**: a fix tuned on one cluster that breaks every
other one. The built-in list is therefore reviewed against installs that are
*not* the one that reported the bug, and `applications` exists so a false
positive is a config edit rather than a release.

## Roadmap

- [x] AEP accepted
- [x] `topology` package + config + hot reload
- [x] `role` on the service-map response
- [x] UI: hide by default, toggle, flow/call split in the count line
- [x] Chart: ungated ConfigMap + values schema
- [x] Collapse the hop: derive `app → app` across a transport span using
      per-trace ancestry, so a meshed cluster regains its real dependencies
      instead of only losing its false ones — shipped in v0.9,
      [AEP](2026-08-25-transport-hop-collapse.md)
- [x] Classify from Kubernetes labels rather than names, once the pipeline
      carries them — the pipeline carries them now, and the answer is narrower
      than this line assumed: labels are decisive for STANDALONE proxies and
      silent for sidecars, whose pod wears the application's labels. So they
      are positive evidence ADDED to the names, never a replacement for them.
      [AEP](2026-08-26-transport-from-labels.md)
