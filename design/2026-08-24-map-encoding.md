# AEP: A map that carries more meaning

- **Date:** 2026-08-24
- **Author(s):** avuru-obs maintainers
- **Status:** Accepted

## Summary

The service map has been a graph of circles since v0.1. It encodes six things
well — health, identity, rate, carbon, call volume and network trouble — and
nothing about *where* a node lives, *how much* traffic an edge carries, or what
sits at the far end of a connection nobody instrumented. This AEP adds those,
under one hard constraint: **no existing channel may be disturbed**, and no new
collection is needed. Everything here is drawn from data the map already
receives, plus one namespace label the hub already queries for the health board.

## Motivation

Two complaints the map earns at scale:

1. **"I can't find anything."** Past ~40 nodes an ungrouped force layout is a
   hairball. The reader knows their estate as namespaces or as service groups;
   the map knows neither, so it cannot lay itself out the way they think.
2. **"What is that edge?"** An edge's volume is only visible on hover, one edge
   at a time, which is useless for the question people actually ask — *which* of
   these paths carries the traffic.

And one thing the map silently throws away: an observed connection whose far end
never sent telemetry is **dropped by the renderer**, because the endpoint is not
in the services list. The hub reported it; the UI deleted it. That peer is
usually the most interesting thing on the screen — it is the part of the estate
nobody has instrumented.

### Goals

- Boundaries: draw namespaces or service groups as containers.
- Edge volume readable across the whole graph at once, not one hover at a time.
- Undetected peers recovered instead of discarded.
- A legend that explains every channel actually in use, and a zoom readout.
- All six existing channels untouched; an ungrouped map renders as before.

### Non-goals

- **External peers are not distinguishable, and this AEP does not pretend
  otherwise.** Naming the far end of a connection that leaves the cluster needs
  a per-address attribute on the flow metric, and the sensor's attribute
  selection deliberately excludes addresses — that per-IP-pair cardinality is
  exactly the defect v0.7 fixed. So a peer we cannot resolve to a workload is
  drawn as **undetected**, which is what we actually know, rather than labelled
  "external", which we would be guessing.
- **No new node channel is spent on grouping.** Boundaries are containers, not
  colours: colour is spoken for.
- **No animated edges.** Motion would compete with the dashed/dotted line styles
  that already carry meaning, and an always-moving graph is tiring to read.

## Solution

### Boundaries (compound nodes)

The map response gains a `namespace` per node, stamped from the *same*
`ServiceLabels` read and the *same* resolution order the health module's
auto-grouping uses (`k8s.namespace.name`, then `service.namespace`) — so a box
on the map and a group on the health board cannot disagree about where a service
lives. Service-group boundaries need no new data at all: the health rollup the
map already fetches for its status rings carries the group.

Grouping is a toolbar select (`Nothing` / `Namespace` / `Service group`) held in
the URL like every other map control, and it defaults to **Nothing** — a box
around every node is only clarifying once you have asked for it.

A service that declares no namespace, and one no group claims, is drawn
**outside** every boundary. Sweeping them into an "other" box would read like a
real place. Virtual targets are never boxed: a derived dependency lives nowhere.

Boundary ids are prefixed with a control character so a box can never collide
with a `service.name`, and boundaries take no pointer events — a container must
never eat a hover meant for the node inside it.

### Edge volume

A toggle puts each edge's rate on the edge itself, for the whole graph at once.
Default off: on a dense map every label is a label too many, and the existing
hover already answers the single-edge question. A flow-derived edge shows bytes
instead of a rate, because it has no calls to count — the same rule the hover
text has followed since v0.2.

### Undetected peers

Any edge endpoint the services list does not contain becomes a node in its own
right, drawn as a dashed outline with no fill. Today the renderer drops those
edges outright, which means an observed connection to an uninstrumented
workload is invisible on the one screen built to show connections.

They are counted apart in the count line — they are not services, and folding
them into that number makes it wrong — and the legend says exactly what they
are: *a peer we have seen traffic to but never heard from*.

### Legend and zoom

The legend gains a line per channel actually in use (it already hides what is
off), and the toolbar gains a zoom percentage beside the zoom controls, so
"fit" and "zoom out" have a number to move.

## Alternatives considered

- **Layout-only grouping** (cluster the layout by namespace without drawing a
  box). Cheaper, but the grouping is then invisible: the reader has to infer it
  from positions, which is exactly what they cannot do on a hairball.
- **An "external" bucket node** collecting everything unresolved. It reads as
  one dependency when it is many unrelated ones, and it hides the count.
- **Always-on edge labels.** Tested badly in the dense case; a toggle keeps the
  clean default and gives the answer on demand.

## Verification

- **Unit (Go):** namespace resolution order and the empty case.
- **Unit (TS):** peer synthesis — which endpoints become undetected nodes, and
  that a fully-known graph gains none.
- **E2E (Playwright):** the grouping select round-trips through the URL and the
  legend follows it; the zoom readout moves with the zoom controls; the edge
  volume toggle; and, with a stubbed map, an unresolved endpoint appears as a
  peer and is counted apart.

## Roadmap

- [x] AEP accepted
- [x] Boundaries, zoom readout, legend
- [x] Undetected peers + edge volume labels
