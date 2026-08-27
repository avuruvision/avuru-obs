# Trace path: the shape of one request

**Date:** 2026-08-27
**Status:** Approved
**Scope:** `ui/` — trace detail panel, new "Path" view

## Problem

A trace could be read two ways, and neither answered *what shape was this
request*.

The **Tree** (and the waterfall, and the flamegraph) is per **span**. It is
exact, and a 300-span trace is 300 cards: the services the request crossed, and
the order it crossed them in, are in there but cannot be seen.

The **service map** is per **estate**. It aggregates every trace in the window,
so it can say what generally depends on what and never what *this* request did.

The gap between them is the ordinary question after opening a slow trace: which
services did it touch, in what order, and where did its time actually go.

## Prior removal — and why this is not a revert

An aggregated cytoscape "Graph" view existed and was **deliberately replaced**
by the Tree in `8419529`, on the grounds that the per-span view is richer and
that aggregated topology belongs on the service map. Both are still true. This
view is a different object: it is per-trace (which the map is not) and per
service (which the Tree is not). The Tree is untouched and keeps its tab.

## Design

`ui/src/lib/trace-path.ts` builds the model; the component only draws it.

### Nodes: services, weighted by self time

A node aggregates one service's spans within the trace. Its weight is **self
time** — each span's duration minus what its children covered — not wall-clock
duration. Duration would double-count, because a caller's span contains its
callee's, and would report the entry service as responsible for the whole
request no matter where the time went.

Depth is counted in **service hops**, not span depth: a service that calls
itself three times internally has not moved further from the entry point. A span
whose parent is absent from the trace marks its service as an entry — that is
not hypothetical, it is what a trace looks like when the true root was exported
to a different backend.

### Exits: the dependencies that never reported

A **Client span with no child in this trace** called something that never sent a
span: a database, a cache, a third-party API. Those become terminal nodes,
drawn dashed and outlined with "no telemetry of its own" — the same treatment
and the same reasoning the service map applies to virtual targets. Dropping them
would end the path at the service that called them and hide the hop the time
often went into.

They are named by what the span actually says: the remote endpoint
(`orders-db:5432`) when there is one, else the technology the component detector
recognised (`PostgreSQL`), else the operation. Never the bare span kind — a node
labelled "Client" names nothing, and is worse than the operation it replaced.

An internal hop (parent and child in the same service) gets **no edge**: a
service must never appear to depend on itself.

### Edges

One per observed caller→callee pair, labelled with the **caller's** duration —
what the caller waited, which is what an edge means everywhere else in this
product. Edges carrying an error are drawn in the error colour, so the failing
branch of a fan-out is visible without reading a single number. Labels are
anchored a third of the way along rather than at the midpoint: two edges
crossing the same gap have different sources, so labels near their source
separate while midpoints collide.

### Focus: the graph answer to "filter by parent"

Selecting **focus** on a node reduces the graph to that service and everything
reachable from it — "show me only what this caused". The walk is guarded by a
visited set rather than assuming a tree, because a path can contain a cycle (A
calls B, B calls back into A). The graph refits on change, or focusing would
strand it off-screen.

Focus is local component state, not URL state: it is an ephemeral lens on an
already-addressed trace, and keeping it out of the URL keeps deep links clean —
the same call `span-detail`'s expand overlay makes.

## Testing

`ui/e2e/trace-path.spec.ts`: the crossed services and the entry marker render;
a dependency with no telemetry appears with its caller-measured time; focusing
drops the caller and clearing restores it; a card selects one of its spans.

Every lookup is scoped to `data-testid="trace-path"` — the summary bar above the
canvas carries a service legend with the same names, so an unscoped text match
is ambiguous by construction.

Verified manually against a seeded ClickHouse on a deliberately branching trace
(gateway → cart → {catalog, pricing} → {inventory, tax}, plus a database exit),
with the failing branch rendered red end to end.
