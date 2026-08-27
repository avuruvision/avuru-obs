# AEP: Trace analytics — asking the trace store a question that is not a list

- **Date:** 2026-08-27
- **Author(s):** Avuru Obs maintainers
- **Status:** Draft

## Summary

Add one grouped-aggregate read over spans — `GET /api/v1/traces/breakdown` — and
the part-of-whole views it feeds: a treemap and a donut on the Traces screen,
grouped by service, operation, outcome, span kind, or any span/resource
attribute, over a chosen span population (requests served, trace entry points,
or every span). It answers *how is my traffic distributed* — a question the
product could not answer at all, having only lists (the operation table) and a
latency distribution (the heatmap).

## Motivation

Every trace surface today returns rows: a trace list, an operation table, a
latency heatmap. Each answers "which requests" or "how slow", and none answers
**"how much of what"**. An operator who wants to know that 60% of their traffic
is one route, or that a service consuming 5% of requests consumes 18% of the
wall time, has to export the operation table and add it up by hand.

The gap is sharpest on the question the estate keeps asking: *where does the
time go?* Volume and time rank differently — a rare, slow operation and a
frequent, fast one look identical in a request count and nothing alike in
seconds — and the product had no view in which the two could be compared.

This is squarely inside the wedge rather than beside it: it reads `otel_traces`,
the table the wedge already fills in the first five minutes. There is no new
collection, no new workload, no new schema and no new module — a fresh install
gets the view with the same install it already ran.

It also unlocks the first honest **root/entry/all** distinction in the product.
`SearchTraces` has always listed one row per trace by its effective root, but
nothing let a reader ask "which services do requests actually *enter* at" as
against "which services *served* something". Those are different questions and
the answers differ by a factor of two on a meshed estate.

### Goals

- One grouped aggregate over spans, filtered exactly like the trace search, so a
  chart and the list beneath it can never describe different traffic.
- Group by service, operation, outcome, span kind, or **any** span or resource
  attribute — the last two are what make it useful past the built-in dimensions.
- Weight by request count **or** total wall time, because they rank differently.
- Three explicit span populations (entry / root / all) rather than a hidden default.
- A part-of-whole chart that is honest about its tail.

### Non-goals

- Time series. The breakdown is one window's distribution; the RED charts
  already own change-over-time.
- A saved-query or dashboard-builder surface. One screen, driven by URL state.
- Per-service or per-trace pages, and an AI-observability module — see
  [Follow-ups](#follow-ups); each is its own change.

## Solution

### Read path

`storage.TraceBreakdown(ctx, BreakdownQuery) (Breakdown, error)` behind the
existing `storage.Store` interface, implemented once in
`hub/internal/storage/clickhouse/breakdown.go`.

The grouping dimension **never reaches SQL as caller text**. It is matched
against a closed set (`BreakdownDimension`), and the two parameterised
dimensions carry their map key as a bound argument like any other value:

```sql
SELECT <dimension> AS k, count(), countIf(<error>), countIf(<refused>),
       sum(Duration), uniqExact(<dimension>), quantiles(0.5,0.95,0.99)(Duration)
FROM otel_traces
WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ? <scope> <filters>
GROUP BY k WITH TOTALS
ORDER BY reqs DESC, k ASC
LIMIT ?
```

Three details carry the design:

- **`WITH TOTALS`** returns the aggregate over every matching span, computed
  before `LIMIT`. Without it a top-N treemap silently redraws its N as the whole
  estate; with it, the API reports the tail as its own bucket and the parts sum
  to the whole. The tail is arithmetic on the totals, not a second query.
- **`uniqExact` over the same expression** is 1 in every grouped row and the
  true distinct-value count in the totals row, where ClickHouse merges the
  aggregate states of all groups. One extra column instead of a second scan.
- **Error and refusal reuse `errorSpanExpr`/`refusedSpanExpr`** verbatim, so the
  breakdown's three-state outcome is the same three states RED, the map's health
  ring and alerting already use. Grouping by the raw OTel `StatusCode` would
  have reported a healthy estate as almost entirely "unset".

### Scopes

`entry` (Server/Consumer), `root` (parentless), `all`. They are not
interchangeable and the UI says so under the control:

| Scope | Counts | The question |
|---|---|---|
| `entry` | Server + Consumer spans | what each service was **asked to do** — the population RED aggregates, so the numbers reconcile |
| `root` | parentless spans | where traffic **entered** the estate, one per trace |
| `all` | every span | span shape; total time double-counts nesting, so never "where does the time go" |

`root` deliberately does **not** fall back to an effective root the way
`SearchTraces` does. That substitution is legitimate when listing traces already
grouped by id; here it would promote an arbitrary child to "entry point" and
invent traffic that never entered there.

### UI

A third tab on the Traces screen, beside Overview and Traces, so the breakdown
inherits the existing filter panel and heatmap rather than starting a screen of
its own. All controls are URL state, so a breakdown is shareable.

- **Treemap** (`ui/src/lib/treemap.ts`, squarified) — area is the weight,
  colour is identity. Failures get a channel of their own: a rule along the
  bottom of each tile as wide a fraction of it as the requests that did not
  succeed. Encoding health as a hue would leave a reader unable to tell "the
  orange service" from "the service in trouble".
- **Donut + legend** — the same ordered groups, for comparing a handful of large
  shares, which is what a treemap is worst at.
- **A table of exact numbers** — and not as a courtesy: three light-mode palette
  steps sit below 3:1 contrast against the card surface, and the table is the
  relief that makes them legal.
- **Drill-down** only where it is faithful. A slice navigates to the trace list
  filtered to exactly what it represents; dimensions with no equivalent filter
  (span kind, and resource keys outside `avuru.tag.*`, which the tag filter
  resolves against the span rather than the workload) are simply not clickable.
  Offering the click would hand back a different set of traces than the one
  pointed at.

### Colour

The first part-of-whole charts in the product need the first categorical
palette. Three schemes, picked by what the dimension **means**
(`ui/src/lib/series-color.ts`):

- **service** → the incumbent `serviceColor` hash. Every other trace view
  already colours a service that way, and a service that is teal in the
  waterfall and blue in the treemap on the same screen is a worse outcome than
  any palette gain.
- **outcome** → the reserved status colours. "error" has one colour in this
  product; a categorical hue for it would be a lie.
- **everything else** → eight hues in a fixed order, declared in `globals.css`,
  assigned by **entity** (a hash of the value) rather than by rank — so a filter
  that changes the ranking does not repaint the survivors. A ninth value shares
  a slot rather than getting an invented hue; the tail takes a reserved neutral,
  because "everything else" is not an identity.

Both modes are validated against the real card surface (`#f8fafc` light,
`#0f1729` dark) on the adjacent-pair CVD, chroma, lightness and contrast checks.
Re-run the check before editing a hex.

### Alternatives considered

- **A second query for the totals** instead of `WITH TOTALS` — twice the scan
  for a number ClickHouse computes for free in the same pass.
- **Truncating without a tail bucket** — the standard failure of top-N charts,
  and the reason a treemap can quietly lie. Rejected outright.
- **Extending `/traces/overview` with a `groupBy`** — it is entry-span-only,
  operation-shaped, and returns no totals. Widening it would have made one
  endpoint answer two questions badly.
- **A new categorical palette for services too**, replacing `serviceColor`. It
  would validate better, but it repaints ten existing components and is a
  cross-screen change that has nothing to do with this feature.
- **A new module.** The breakdown reads `otel_traces` and adds no collection, so
  gating it would only hide a view an install already has the data for.

## Verification

- **Unit (hub)** — `internal/api/breakdown_test.go`: the closed dimension set
  rejects a column name and an injection attempt with 400; every dimension and
  scope parses; the trace filters reach the query; the tail is derived from the
  totals and carries no quantiles (they cannot be recovered by subtraction); the
  empty key survives.
- **Integration (real ClickHouse 26.3)** —
  `internal/storage/clickhouse/breakdown_integration_test.go`: totals cover
  groups past the limit; the three scopes count different spans; outcome
  separates refused from error; a key crafted as SQL is inert; filters and the
  aux exclusion bite; an empty window is an empty breakdown, not an error.
- **E2E (Playwright)** — `ui/e2e/breakdown.spec.ts`: both charts and the table
  render; the scope selector drops a callee from the entry-point view; a row
  drills to the filtered trace list; controls survive a shared URL.
- **Manual** — verified against a seeded ClickHouse in both themes, including
  the request-vs-time reweighting (a service at 4.9% of requests and 17.9% of
  wall time) that motivates the second weighting.

The wedge is unaffected: no new collection, no new container, no schema change.

## Follow-ups

Named here so the direction is recorded, each its own change:

- **A service detail page.** Clicking a map node or a service row lands on
  filtered traces today; there is no page that puts one service's RED, its
  dependencies, its traces, its logs, its pods and its cost side by side. Every
  endpoint it needs already exists.
- **A per-trace dependency map.** The trace views draw a waterfall, a tree and a
  flamegraph — all time-ordered. The path a single request took through the
  estate is a graph, and the map component already renders one.
- **AI observability.** `SpanAttributes` is a full map, so `gen_ai.*` spans are
  already queryable with no schema change; token counts, model mix and per-model
  latency are a module on top of data the gateway would already be storing.
- **A better service hue.** `serviceHue` maps names onto a continuous range and
  neighbouring services can land within a few degrees — tolerable in a
  waterfall, visible in a treemap.

## Roadmap

- [ ] AEP accepted
- [x] `TraceBreakdown` storage read + ClickHouse implementation
- [x] `GET /api/v1/traces/breakdown`
- [x] Treemap, donut and exact-numbers table on a Breakdown tab
- [x] Categorical palette, validated in both modes
- [ ] Docs site: feature page + API reference (bilingual)
