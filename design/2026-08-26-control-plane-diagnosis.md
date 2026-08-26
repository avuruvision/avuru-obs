# AEP: Why the control plane is silent

- **Date:** 2026-08-26
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

The mesh screen's control-plane card has two states — observed, or not — and
"not" covers three completely different problems: nothing is scraping it, the
scrape target is not answering, and the target answered with metrics this
product cannot read. Split them, using the scrape-report series the pipeline
already stores, and say plainly that the control-plane half is Istio-shaped so
an operator running something else learns it from the product rather than from
an empty card.

## Motivation

[The mesh AEP](2026-08-25-mesh-surfaces.md) established the honest-absence rule:
a control plane nobody scrapes reports zero rejected configs, which reads as
perfect health, so absence is *stated* rather than rendered as zeros. That rule
is right and it is doing its job. What it cannot do is tell the operator **why**
nothing is there — and each of the three reasons has a different fix:

| What happened | What to do about it |
|---|---|
| Nothing is scraping | set `mesh.controlPlane.enabled=true` |
| The target is not answering | fix the endpoint, or the control plane is down |
| The target answered, nothing recognisable came back | it is not a control plane this product can read |

Today all three render the same sentence. The third is the one the roadmap
named: *"the control-plane half is Istio-shaped and says so"* — except that it
does not say so, it just goes quiet, which is indistinguishable from a
misconfigured endpoint.

### Goals

- **Three diagnoses instead of one**, each naming its own fix.
- **Say which control plane is understood.** An operator running Linkerd should
  learn that from the screen, not from reading the metric names in our source.
- **No new collection.** The signal is already in the tables.

### Non-goals

- **Reading Linkerd's control plane.** See the finding below: this is not a
  metric-name mapping exercise, and pretending otherwise would produce a card
  full of numbers that mean something else.
- **Auto-detecting the mesh flavour.** Nothing to detect while exactly one
  flavour is implemented; a `flavor` value would be a knob with one setting.

## Solution

### The finding: Linkerd does not publish the same four facts

The Istio card answers four questions — proxies connected, pushes attempted,
configuration **refused**, convergence latency — and they come from
`pilot_xds`, `pilot_xds_pushes`, `pilot_total_xds_rejects` and
`pilot_proxy_convergence_time`.

Linkerd's destination controller does not have counterparts. Its Prometheus
surface is dominated by Go and gRPC runtime series plus a few controller
specifics (`endpoint_updates_queue_overflow` and similar); there is no
"configuration the proxies refused", which is the single most valuable number
on the card and the one nothing else in the product can produce.

So supporting Linkerd is not a mapping table. It is a design question — *which
Linkerd signals answer which question, and which questions simply have no
answer there* — and it needs someone running Linkerd to state it. Until then
the honest move is to say the card is Istio's, in the product, where the
operator is standing.

### The signal that was already there

The gateway's `prometheus/mesh` scrape emits Prometheus's synthetic
scrape-report series, and they **bypass `metric_relabel_configs` by design** —
which is why the green module's pipeline drops them explicitly and this one does
not. So `up` for the control-plane job is already in `otel_metrics_gauge`,
carrying `service.name` = the job name (verified in the receiver's
`CreateResource` at the pinned collector tag: `job` → `service.name`,
`instance` → `service.instance.id`).

That is exactly the missing signal, and it costs one query:

- no `up` rows → **unconfigured**
- latest `up` is 0 → **unreachable**
- latest `up` is 1, no `pilot_*` in the window → **unrecognised**
- known metrics present → **ok**

`MeshControlPlane` gains `State` and `Kind` ("istio" when recognised).
`Available` stays exactly as it was — `State == "ok"` — so nothing that already
reads it changes meaning.

The job name becomes a value (`mesh.controlPlane.jobName`, default `istiod`)
because the hub has to look the `up` series up by it; leaving it hardcoded in
two places is how the two stop agreeing.

### Alternatives considered

- **Map Linkerd's metrics onto the Istio card anyway.** The four labels would
  stay and the numbers underneath would answer different questions. This
  codebase has been burned twice by config that looked right from the outside;
  this would be the same mistake with a friendlier face.
- **A `flavor` value with `istio` and `generic`.** `generic` would recognise
  nothing, so every install would report `unrecognised` — a knob whose only
  other setting is the state you are already in.
- **Detect the flavour from the scraped series.** Worth doing the moment there
  is a second flavour. With one, it is indirection around a constant.
- **Drop the scrape-report series to save cardinality.** Two series per scrape,
  and they are the entire diagnosis. Keeping them is the cheapest thing on this
  page.

## Verification

- **ClickHouse integration**: each of the four states from real rows —
  including `up = 1` with no `pilot_*`, which is the state that did not exist
  before.
- **Handler**: each state maps to its own reason string, and `available` still
  means exactly what it meant.
- **Playwright**: the unrecognised card names Istio and does not render an
  empty grid of zeros.
- **Chart**: the job name reaches both the scrape config and the hub, from one
  value.

## Roadmap

- [x] AEP accepted
- [x] `up`-based state in `MeshControlPlane`
- [x] Job name as one value, reaching the scrape and the hub
- [x] API: state + kind, `available` unchanged
- [x] UI: three cards, and the Istio-shaped limitation stated in the product
- [ ] Read a second control plane, once someone running one can say which of
      its signals answer these questions
