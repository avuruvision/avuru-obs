# AEP: Collapsing the transport hop

- **Date:** 2026-08-25
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

On a meshed cluster the service map draws `app → proxy → app`. Since v0.8 it
hides the proxy and both of its edges, so the false dependencies are gone — and
the real one underneath them is gone too. This AEP recovers `app → app` by
walking each trace's own parent chain across the transport spans, and states the
rule that stops the same request being drawn twice.

## Motivation

[Transport workloads on the service map](2026-08-23-service-map-transport.md)
classified mesh proxies and gateways and hid them by default. That fixed the
lie. It did not restore the truth: on a cluster where every call is intercepted,
hiding the interceptor leaves a map of disconnected circles. The install that
reported eleven services and two wrong edges would now see eleven services and
*no* edges — more honest, equally useless.

The parent AEP deferred this deliberately, and named the reason:

> Reconstructing `app → app` from `app → proxy → app` needs per-trace ancestry,
> and the naive version — pairing every inbound edge of a proxy with every
> outbound one — invents an N×M cross-product of edges that are as fictional as
> the ones being removed.

That objection is precise, and it is an objection to *aggregate* pairing, not to
collapsing as such. A proxy that received from 4 callers and sent to 5 backends
has 20 possible pairs and at most 20 real ones — but which 20 is not knowable
from the two edge sets. It **is** knowable from the trace: every Server span has
exactly one parent, so following the chain gives one caller per call, never a
product. This AEP does that.

### Goals

- A meshed cluster gets its application dependencies back, drawn like any other.
- A request is drawn once: either as its hops or as the collapsed dependency,
  never both.
- The reader can tell a collapsed edge from a directly-traced one, and see which
  proxy it went through.
- A cluster with no mesh runs no extra query and returns identical bytes.

### Non-goals

- **Changing `ServiceEdges` or `NetworkEdges`.** The collapse is a new query
  beside them; the shipped ones keep their population and their tests.
- **Collapsing flow-derived edges.** Kernel flows carry no trace ancestry —
  there is nothing to walk. A meshed cluster's flow edges stay hops, and stay
  hidden with the proxies.
- **Unbounded ancestry.** See the depth bound below.
- Mesh observability as a feature — that is
  [mesh-facing surfaces](2026-08-25-mesh-surfaces.md).

## Solution

A new store method walks the chain in SQL:

```
CollapsedEdges(ctx, q ServiceQuery, transport []string) ([]ServiceEdge, error)
```

The hub already classifies every service on the map (`hub/internal/topology`).
The handler now does it *before* querying edges and passes the resulting
transport names down, so one map cannot disagree with itself about what a proxy
is — the classification stays in Go, in one place, and SQL receives a list of
names rather than a second copy of the glob logic.

For each depth *k* in 1..3 the query joins a Server span to its parent, that
parent to *its* parent, and so on through exactly *k* spans whose `ServiceName`
is in `transport`, terminating at a Client span whose service is not. The
branches are `UNION ALL`ed as raw rows and aggregated once, so latency
quantiles are computed over the whole population rather than merged per branch.

**Why the branches cannot double-count.** Depth *k* requires the *k*-th
ancestor to be transport and the (*k*+1)-th not to be; depth *k*+1 requires the
(*k*+1)-th to be transport. A span is either in the transport set or it is not,
so a given Server span matches exactly one branch.

**Why depth 3.** A sidecar mesh interposes two proxy spans (egress + ingress);
Istio ambient interposes up to three (client ztunnel → waypoint → server
ztunnel). Three is the deepest real topology we know of, and each extra level
is another self-join over `otel_traces`. It is a constant with that rationale
next to it, not a knob: an operator cannot usefully guess it, and the cost of
being wrong is paid on every map render.

**The caller must be a Client span**, exactly as `ServiceEdges` requires. That
keeps the two queries counting the same kind of thing, and stops the collapse
inventing edges the direct path would never have drawn.

**Latency is the originating client span's duration** — again as `ServiceEdges`
does. On a collapsed edge that figure includes the proxies' own overhead, which
is correct: it is what the caller waited for.

**Cost.** `len(transport) == 0` returns nil without touching the database. A
cluster with no mesh pays nothing and its `/api/v1/service-map` response is
byte-identical to v0.8's.

### The double-count rule

A collapsed `A → B` and the hops `A → proxy`, `proxy → B` describe the *same
requests*. Drawing both would triple the apparent traffic.

The API returns everything, as it always has — the hub reports, the client
decides. The rule lives in the one place that already makes this decision,
`splitInfrastructure`:

| View | Transport nodes | Hop edges | Collapsed edges |
|---|---|---|---|
| default | hidden | hidden (they touch a hidden node) | **shown** |
| `?infra=true` | shown | shown | **hidden** |

So the toggle swaps representations rather than accumulating them, and the
count line stays a count of requests rather than of drawings.

### Rendering

A collapsed edge **is** a dependency and is drawn as one — no new visual
channel, the six load-bearing ones (ring, fill, size, halo, width, line colour)
are untouched. It carries `viaTransport`, which the hover appends as
`via istio-proxy`, and the count line says how many edges were recovered through
the mesh. An `A → B` pair that has both direct and collapsed calls (a mesh with
exclusions) sums its counts and keeps `provenance: "trace"`, with
`viaTransport` still populated: part of that traffic really did go through a
proxy, and hiding that would be the same class of error as the one being fixed.

### Alternatives considered

- **Pair the proxy's edges in aggregate.** The N×M invention the parent AEP
  named. Rejected there, rejected here.
- **Walk the ancestry in Go.** Needs every span in the window shipped to the
  hub to reconstruct chains the database can join in place — and puts the hub
  in a data path it has no business being in.
- **`WITH RECURSIVE`.** Expresses unbounded depth honestly, but an unbounded
  walk over `otel_traces` is exactly the cost we want bounded, and the bound is
  a product decision rather than a query-planner one.
- **Collapse server-side and drop the hops.** Makes the mesh unobservable and
  bakes a view decision into the API — the same argument that kept the parent
  AEP's classification advisory.

## Verification

- Storage integration (real ClickHouse): a sidecar-shaped trace
  (`A client → proxy → B server`) yields one `A → B` edge with
  `viaTransport: ["proxy"]`; an ambient-shaped trace with three proxy spans
  yields one edge and all three names.
- **The N×M test**, which is the whole objection: two callers through one shared
  proxy to two backends must produce exactly the two edges that happened, not
  four. This test is the reason the feature is allowed to exist.
- No transport in the window → no query issued and no edges returned.
- Handler tests: `viaTransport` and `provenance` reach the JSON; an unmeshed
  map is unchanged.
- Playwright: the collapsed edge is on the map by default, and toggling
  **Show mesh & gateways** replaces it with the two hops rather than adding
  them.

## Roadmap

- [x] AEP accepted
- [x] `CollapsedEdges` + depth ladder + transport guard
- [x] Handler: classify before querying, merge, `viaTransport` on the DTO
- [x] UI: draw collapsed edges, the double-count rule, hover + count line
- [x] Tests: integration (incl. N×M), handler, Playwright
- [ ] Classify from Kubernetes labels rather than names, once the pipeline
      carries them (inherited from the parent AEP)
