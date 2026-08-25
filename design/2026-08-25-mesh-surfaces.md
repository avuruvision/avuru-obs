# AEP: Mesh-facing surfaces

- **Date:** 2026-08-25
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

For clusters where the service mesh *is* the network, the mesh is not
background infrastructure — it is the thing that breaks. avuru-obs has spent two
releases learning to see past the mesh: v0.7 hid the proxies, v0.9 recovered the
dependencies behind them. This AEP gives the mesh a screen of its own: how the
proxies are doing, and whether the control plane is still programming them.

## Motivation

The transport classification exists and is load-bearing — it decides what the
map hides and what the hop-collapse walk steps over. Everything it identifies is
then *removed from view*, which is right for a dependency graph and wrong as the
final word. A proxy that is dropping requests, or a control plane that has
stopped pushing configuration, is the outage; the map's job is to not blame the
application for it, not to pretend it does not exist.

Two questions have no home today:

1. **Are the proxies healthy?** They emit spans like any workload — rate, errors
   and latency for every one of them are already in ClickHouse. Nothing reads
   them, because every surface that could either hides transport or lumps it in
   with applications.
2. **Is the control plane still programming the mesh?** This one avuru-obs
   genuinely cannot answer from what it collects. A proxy fleet keeps serving
   its last good configuration long after istiod stops pushing, so silence from
   the control plane looks exactly like health right up until the first
   deployment that never takes effect.

### Goals

- Proxy load, latency and success rate, per proxy, from telemetry already
  stored.
- Control-plane health: connected proxies, push convergence, and configuration
  the proxies **rejected** — the signal that says the mesh and its control plane
  disagree.
- Honest absence: with no control-plane scrape configured, the screen says so
  rather than reporting zeros.
- Nothing on a cluster with no mesh: no nav entry, no routes, no scrape.

### Non-goals

- **Per-proxy configuration inspection** (what routes a sidecar has). That is
  `istioctl`'s job, needs the proxy admin API, and is a debugging tool rather
  than an observability surface.
- **Mesh-specific tracing.** Proxy spans already flow through the normal trace
  pipeline; there is nothing mesh-shaped to add.
- **Non-Istio control planes.** Linkerd and Cilium expose different metrics
  under different names. The proxy half works for any of them (it is just RED on
  classified workloads); the control-plane half is Istio-shaped and says so.

## Solution

### A `mesh` module

Registered in `hub/internal/modules`, following the `service-health`
precedent exactly: it gates API routes, the UI surface and one collection job,
and owns no schema. Off by default — most installs have no mesh, and a module
that lights up a nav entry for infrastructure you do not run is the thing v0.8's
navigation work set out to stop.

### The proxies: no new collection

`GET /api/v1/mesh/proxies` returns RED for the workloads
`hub/internal/topology` classifies as transport, plus the edges they carry.
That is `ListServices` and the classifier the service map already builds, read
with the opposite filter — the same data, inverted. A proxy's own error rate and
p95 are its health; the edge counts either side of it are its load.

Deliberately no new SQL: if this needed a query, it would be a sign the
classification was in the wrong place.

### The control plane: one scraper, in the gateway

istiod publishes Prometheus metrics on port 15014. Three of them answer the
question:

| Metric | Question |
|---|---|
| `pilot_xds` | How many proxies are connected right now |
| `pilot_proxy_convergence_time` | How long a config change takes to reach them |
| `pilot_total_xds_rejects` | How much config the proxies **refused** |

The third is the one that cannot be inferred from anywhere else. A rejected
push means the control plane and the data plane disagree about what the mesh
should be doing, and the fleet keeps serving the last config it accepted.

**Where the scrape runs is the design decision.** The only Prometheus scraping
avuru-obs does today is `prometheus/green` inside the sensor **DaemonSet**,
against `127.0.0.1` — correct there, because Kepler and the TDP estimator are
per-node sidecars. istiod is one Deployment. A DaemonSet scraping it would
produce one copy of every series per node, and a sum over them would be wrong by
a factor of the cluster size.

So the scrape goes in the **gateway**, which is already a single-writer
Deployment: `prometheusreceiver` joins the OCB manifest and a static-target
scrape job is rendered under `mesh.controlPlane`.

The cost is honest and worth stating: the OCB distro is deliberately minimal and
`prometheusreceiver` is not a small module. The alternative — a dedicated
single-replica scraper Deployment — keeps the gateway lean at the price of
another workload, another Service account and another thing to fail. One
receiver in a collector that already exists beats a new pod for one scrape
target.

`GET /api/v1/mesh/control-plane` reads those series back out of
`otel_metrics_*`, so the module requires `infra-metrics` — enforced by a chart
guard, in the same style as the flows and green guards.

### Honest absence

The endpoint returns `available: false` with a reason when no control-plane
series exist in the window, and the UI renders that as a stated gap rather than
as zeros. A control plane reported as "0 rejected configs" when nothing is being
scraped is exactly the failure mode this surface exists to prevent — the same
argument that made the green module report "no RAPL" instead of 0 W.

### Alternatives considered

- **Scrape from the sensor DaemonSet.** Reuses the shipped mechanism; multiplies
  every control-plane series by node count. Rejected.
- **Fold the proxies into the service map's `?infra=true` view.** Cheaper, and
  the map is the wrong shape for it: a table sorted by error rate is how you
  find the bad proxy in a fleet of forty, and the map cannot be that.
- **Envoy ALS** (the `envoy_als` receiver) for mesh topology. Alpha, logs-only,
  and topology is precisely the problem hop collapse already solved from traces.
  Nothing left for it to do here.
- **No control-plane half.** Half the value: the proxy metrics are the part
  another tool could give you.

## Verification

- Unit: the classifier split (proxies in, applications out) and the
  control-plane roll-up from raw series, including the empty case reporting
  `available: false` rather than zeros.
- Handler: routes absent when the module is off; the proxy list carries only
  transport workloads; a `topology` config override reaches this screen as it
  reaches the map.
- Storage integration: control-plane series aggregate to connected proxies,
  convergence p95 and rejected-config count against real ClickHouse.
- Chart render: the receiver and scrape job appear only with
  `modules.mesh.enabled` + `mesh.controlPlane.enabled`; the infra-metrics guard
  fires; nothing renders by default.
- Playwright: the screen lists proxies and states the gap when the control plane
  is not scraped.

## Roadmap

- [x] AEP accepted
- [x] `mesh` module + capabilities + nav entry
- [x] `GET /api/v1/mesh/proxies` from the existing RED + classifier
- [x] `prometheusreceiver` in the gateway distro + `mesh.controlPlane` scrape
- [x] `GET /api/v1/mesh/control-plane` + honest absence
- [x] UI: proxy table + control-plane card
- [ ] Linkerd / Cilium control planes, if asked for
- [ ] Per-proxy configuration inspection — out of scope, may never be in it
