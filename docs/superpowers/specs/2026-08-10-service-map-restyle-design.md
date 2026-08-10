# Service map restyle (v0.5 W7)

**Date:** 2026-08-10
**Status:** Approved, ready for implementation
**Workstream:** v0.5 "operate it from the UI", W7
**Branch:** `feature/service-map-restyle`

## Problem

[`service-map.tsx`](../../../ui/src/components/service-map/service-map.tsx) draws
a correct graph that answers almost nothing. Nodes are gold circles sized by
request rate and turned red when *any* error occurred in the window — a binary
that says "something failed" without saying whether the service is actually in
trouble. Edges are grey lines whose only numbers hide in a hover tooltip. There
is no legend, no way to narrow the view, and no fit control, so on an estate of
any size the map is a picture of the architecture rather than a diagnostic.

Meanwhile the hub already computes an authoritative per-service health status
([`health/status.go`](../../../hub/internal/health/status.go)) that the map
ignores, and the Service Map screen and the Dashboard's topology card render the
same component — so one restyle improves both surfaces.

## Goals

- A node says how the service is *doing*, using the same verdict the Service
  Health screen shows — not a second, browser-invented one.
- An edge says how much traffic it carries and how slow that specific call path
  is.
- The map stays readable as the estate grows: quiet at rest, informative where
  the user is looking, and narrowable.
- The Dashboard's compact map improves for free.

## Non-goals

- No new topology implementation. Cytoscape + fcose stays.
- No compound/grouped nodes (group *filtering* covers the need without risking
  the layout).
- No user-composable dashboard widgets — deferred to v0.6 with the rest.
- No change to what a click on a node does (it still opens that service's
  traces).

## Design

### 1. Hub — real per-edge latency

`ServiceEdges` already self-joins client and server spans and groups by
`(src, dst)`. Per-edge quantiles are one more aggregate over rows the query
already produces — no new join, no second scan:

```sql
quantiles(0.5, 0.95)(toFloat64(client.Duration)) AS qs
```

**`client.Duration`, not `server.Duration`.** The client span measures what the
caller experienced on that call path — network, queueing and the callee's own
work. That is a different number from the callee's server-side p95, which the
node already shows, so the edge carries information instead of repeating the
node's. It is also the number that explains a slow caller when the callee looks
healthy overall.

Changes:

| File | Change |
|---|---|
| `hub/internal/storage/store.go` | `ServiceEdge` gains `P50, P95 time.Duration` |
| `hub/internal/storage/clickhouse/services.go` | add the `quantiles(...)` select + scan via the existing `nsQuantiles` helper |
| `hub/internal/storage/storagetest/fake.go` | carry the new fields |
| `hub/internal/api/traces.go` | `mergeEdges` preserves trace quantiles on `"both"` edges |
| `hub/internal/api/dto.go` | `p50Ms`, `p95Ms` — both `omitempty` |
| `ui/src/lib/api-types.ts` | mirror as optional `p50Ms?`, `p95Ms?` |

Flow-only edges (OBI network flows with no trace spans) have no client span, so
their quantiles stay zero and `omitempty` drops the fields entirely. The UI must
treat absent latency as "not measured", never as zero.

### 2. Node status — read the hub's rollup

A new `useServiceHealthStatus(time, includeAux)` hook flattens
`/api/v1/health/groups` `groups[].members[]` into
`Map<service, { status, reason, group, tier }>`, using `effectiveStatus` (the
dependency-aware verdict) so a service dragged down by a dependency reads the
same on the map as it does on the health board.

The map must not re-derive thresholds. They are configurable per group, live in
the hub, and a second copy in the browser would drift.

Ring colors come from the existing
[`lib/health-status.ts`](../../../ui/src/lib/health-status.ts) vocabulary
(`statusTone`/`statusDotClass`), so the map and the health board cannot disagree
about what amber means. A service the rollup does not cover — on the map but in
no group, or aged out of the health window — is `unknown` and takes the neutral
ring, exactly like `idle`. Absent status is never rendered as healthy.

**Module gating.** `/api/v1/health/groups` 404s when the `service-health` module
is off, and hooks cannot be called conditionally. The query therefore lives in a
child component that only mounts when the module is enabled — the pattern
established by
[`summary-band.tsx`](../../../ui/src/components/dashboard/summary-band.tsx).
With the module off, the ring falls back to today's binary error presence and
the legend says "errors" rather than naming statuses.

**Cost.** The Dashboard's band 1 already issues this exact query with the same
key, so the compact map's rings cost nothing there. On the Service Map screen it
is one additional cached request.

### 3. Visual channels

The carbon lens currently owns `border-color`, which is where a status ring
belongs. Status is always on; carbon is an optional lens — so status takes the
ring and **carbon moves to a halo** (`underlay-color`, `underlay-padding`,
`underlay-opacity`). The stable channel stays stable, and the lens reads as
something laid over the map rather than a repaint of it.

| Channel | Encodes | Change |
|---|---|---|
| node ring | health status: success / warning / error / muted idle | new |
| node fill | service identity (primary) | replaces the `error > 0` red fill |
| node size | request rate | unchanged |
| node halo | gCO2e bucket, carbon lens only | moved off the border |
| edge width | call volume | unchanged |
| edge color | plain · amber = network health · red = errors | unchanged |
| arrowhead | direction | unchanged |

The carbon overlay keeps its existing contract: absent on a non-green install,
and inert when the lens is off.

### 4. Hover-focus

At rest the map is quiet: node name, ring, size. On node hover:

- the hovered node and its 1-hop neighbours stay lit; everything else fades to
  ~18% opacity;
- its edges thicken and animate their dash offset along the direction of the
  call, so direction reads as motion rather than only as an arrowhead;
- its edges gain a label — `120 rpm · p95 310ms`, plus `2.1% err` when the error
  rate is non-zero and `RTT 12ms` when OBI measured one;
- the hovered node's own label expands to `name · 42 rpm · p95 88ms`.

Implemented with cytoscape classes (`.focus`, `.related`, `.faded`) applied on
`mouseover`/`mouseout`, not by rebuilding elements — the layout must not move.

RPM is derived in the browser from `calls / windowMinutes`; the window comes
from the screen's existing time range, passed to the component as `windowMs`.
Node RPM is `ratePerSec * 60`.

**Honest limitation:** focus is hover-driven and therefore inert on touch. The
always-on ring, node labels and the legend carry the meaning there. Click
continues to navigate to the service's traces on every input type.

### 5. Toolbar, filters, legend

All filter state lives in URL params, per the house rule that an observability
URL must be pasteable into Slack:

| Control | Param | Behavior |
|---|---|---|
| Search | `q` | case-insensitive substring on service name |
| Problems only | `problems=true` | keeps degraded + down (offered only when the health module is on) |
| Group | `group=<name>` | keeps that group's resolved members (health module only) |
| Carbon | `carbon` | existing |
| Show auxiliary | `includeAux` | existing |

Filtering drops non-matching nodes and any edge touching them, and updates the
`N services · M edges` count line. That line is the assertion surface: cytoscape
draws to a canvas, so the count text is what proves a filter worked in Playwright.

Group membership comes from the health rollup's resolved `members[]`, not from
`ServiceGroupDef.services/namespaces` — the rollup is what actually grouped the
services, including namespace matches.

Controls: zoom in, zoom out, **fit** (`cy.fit`), and the existing re-layout.

The legend is one dense row: ring colors with their status names, `size = rate`,
`width = calls`, `dashed amber = network health`, `red = errors`, and the carbon
halo when the lens is on.

### 6. Compact mode (Dashboard)

No toolbar, no legend, names only — plus rings, hover-focus and an auto-fit on
mount. Unchanged in every other respect: it stays the same component in a
smaller mode, so W7 lands on both surfaces at once.

`TopologyCard` must thread `windowMs` down from the Dashboard's existing time
range (it currently passes only `services` and `edges`), otherwise the compact
map's focus labels have no denominator for RPM.

### 7. File split

`service-map.tsx` is 317 lines and this would roughly double it, past the ~300
line house limit. Split along the seams the restyle creates:

```
ui/src/components/service-map/
  service-map.tsx           React shell + cytoscape lifecycle
  graph-style.ts            theme tokens + stylesheet, incl. focus/fade classes
  graph-elements.ts         services + edges + status → cytoscape elements, labels, tooltips
  graph-focus.ts            hover-focus class application
  map-toolbar.tsx           search, problems-only, group select, zoom/fit/relayout
  map-legend.tsx            legend row
  service-map-screen.tsx    composes toolbar + legend + graph (existing file)
ui/src/hooks/
  use-service-health-status.ts
ui/src/lib/
  map-filter.ts             pure: (services, edges, filters) → subset
```

`map-filter.ts` is deliberately pure and dependency-free so the filter logic is
readable and reviewable on its own, independent of cytoscape.

## Testing

**Go**

- `mergeEdges` preserves trace-derived quantiles when a flow edge merges in as
  `"both"`.
- `router_test` asserts `p95Ms` survives the store → DTO → JSON path, and that a
  flow-only edge omits it.
- Extend the existing clickhouse integration test to cover the new quantile
  columns (`TESTCONTAINERS_RYUK_DISABLED=true` on this machine).

**Playwright** — new `ui/e2e/service-map.spec.ts`:

- legend row renders and names the statuses;
- search narrows the `N services · M edges` count and round-trips through the
  URL;
- problems-only and group filters likewise;
- zoom/fit/re-layout controls are present and clickable;
- the Dashboard's compact map still renders (`service-map-compact`).

**Two existing specs break on purpose** and must be updated in the same change:

- [`green.spec.ts:394`](../../../ui/e2e/green.spec.ts) asserts the literal
  string `"Carbon lens on · node border = gCO2e"`; the caption becomes *halo*.
- `green.spec.ts:432`'s node-hover tooltip test now shares `mouseover` with
  focus. It is already flake-flagged; re-verify it, and if focus destabilizes
  it, downgrade to the toggle+legend contract as that spec's own note allows.

**Gates:** `make check`, `make e2e`, `make e2e-ui`; `cd hub && golangci-lint run`
before pushing Go. The UI has no unit-test runner, so pure helpers are covered
through the e2e surface, not in isolation.

## Alternatives considered

**Hand-rolled SVG/canvas renderer** over `use-pan-zoom` and the traces
primitives, as the v0.5 plan suggested. Rejected on inspection: the map is
cytoscape + fcose, which already owns pan, zoom, fit and force layout. Rebuilding
those to reuse a hook would trade a working layout engine for weeks of work.

**Legend, filters and fit only**, no visual restyle. Cheap and safe, but leaves
the map exactly as unreadable as it is now — it fails the goal.

**Server-side edge latency (`server.Duration`).** Rejected: it duplicates what
the callee node's p95 already says. Client-side is the number that isolates a
slow path.

**Status as node fill, identity dropped.** Rejected: fill is the strongest
channel and it is already carrying the carbon lens's neighbour; overloading it
would make a healthy estate read as a wall of green blobs with no service
identity. Ring reads as a status annotation on an identifiable node.

**Compound nodes per service group.** Rejected for v0.5: fcose handles compound
graphs poorly at this size and the group *filter* answers the same question
without risking a tangled layout.

## Follow-on

Per house rule, W7 is not shipped until the docs site says so: run the
`docs-align` skill (EN + FR) for the restyled map before closing the workstream.
