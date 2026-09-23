# Service detail page: one service, all its signals

**Date:** 2026-08-27
**Status:** Approved
**Scope:** `ui/` — Services screen, service map click target

## Problem

There was nowhere to look at *one service*. Clicking a row in the inventory or a
node on the map dropped the user into `/traces?service=X` — a filtered trace
list, which answers "what did it serve" and nothing else. To ask the ordinary
follow-up questions (who calls it, what it depends on, is it healthy, what is it
logging, what is failing) meant visiting four screens and re-applying the same
filter in each.

Every number needed already existed; nothing joined them.

## Design

### Route: a query parameter, not a segment

`/services?service=<name>` renders the detail; `/services` renders the
inventory. **Not** `/services/[name]`: the UI is a static export
(`output: 'export'`), so a dynamic segment needs `generateStaticParams`, and the
set of services is discovered at runtime by definition — it cannot be enumerated
at build time. Selection-as-URL-state is also what every other screen here does.

This keeps the existing e2e assertion (`toHaveURL(/service=<name>/)`) true, and
the breadcrumb still resolves, because `routeInfo` already matches on prefix.

### Composition, not a new endpoint

The page issues no request the product did not already make:

| Section | Source |
|---|---|
| RED headline + role + namespace | `GET /api/v1/service-map` (the node) |
| Called by / Depends on | the same response's edges, filtered client-side |
| Health status + reason | `GET /api/v1/health/groups`, module-gated |
| Rate/errors/duration over time | `GET /api/v1/metrics/red?service=` |
| Traces / Logs / Errors tabs | the existing search endpoints, service-filtered |

**Dependencies must come from the map's edge set.** That response is where
transport classification and hop collapse are applied
(`design/2026-08-25-transport-hop-collapse.md`); a per-service dependency query
would eventually disagree with the map about what depends on what, and the two
screens would describe the same estate differently. Reusing it also means
arriving from the map is a cache hit rather than a second fetch.

### Honesty rules carried over

- An edge's `p95Ms` is **absent** on flow-derived edges — "not measured", never
  `0ms`. It renders as `—`.
- A dependency the hub recovered across a proxy is labelled `via <proxy>`, so a
  reconstructed edge never reads as a directly observed one.
- A name with no service behind it in the window gets a named empty state
  ("stopped reporting, or the window predates it"), not empty cards that read as
  "this service does nothing".
- Virtual targets remain unclickable on the map: they send no telemetry, so
  every panel on their page would be empty or filled with their callers'
  numbers.

### Signals: reuse, then leave

The Traces, Logs and Errors tabs mount the components that own those views
(`TraceList`, `LogTable`, `IssueList`) with a service filter. Selecting a row
**navigates to the screen that owns the detail** rather than embedding it: the
trace workspace and the issue panel are large stateful surfaces, and a service
page that swallowed both would be two screens wearing one URL.

Tabs for `logs` and `error-tracking` are hidden when the module is off — the
same `useCapabilities` gate the sidebar uses.

## Testing

`ui/e2e/service-detail.spec.ts`: the header and RED tiles render; the caller
appears under "Called by" and clicking it opens *that* service's page; the
signal tabs are service-scoped; an unknown name gets the empty state and a way
back. `ui/e2e/services.spec.ts` updated — the row click now lands on the detail.

Verified manually against a seeded ClickHouse: caller, callee and virtual-target
rows populate with per-edge rate/errors/caller-side p95, and the health badge
carries the evaluator's own reason string.
