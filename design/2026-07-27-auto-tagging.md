# AEP: Richer auto-tagging — Kubernetes labels/annotations as business tags

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Accepted — implemented 2026-08-23, with the scope corrections below

## Summary

Turn selected Kubernetes labels and annotations into first-class **business tags**
carried on every signal, so telemetry can be sliced by `team`, `tier`,
`cost-center`, `owner` — the vocabulary an org already stamps on its workloads —
instead of only by service/namespace. The mapping is chart-configured, applied in
the gateway by the collector's `k8sattributesprocessor` (already in the distro),
normalized to a stable `avuru.tag.*` namespace, and exposed as filters across
traces, logs, metrics, profiles and the service map.

## Motivation

Services already carry rich Kubernetes metadata, but the UI only groups by service
and namespace. Platform teams think in terms the cluster already encodes as
labels/annotations (`team=payments`, `tier=critical`, `app.kubernetes.io/part-of`).
Surfacing those as queryable tags is high leverage — no new instrumentation, it
rides the metadata eBPF/k8sattributes already collect — and it feeds the
[service-health groups](2026-07-18-service-health-groups.md) (config selectors can
match a tag instead of enumerating services) and future per-tag alerting. Ties to
the [wedge](../AGENTS.md): zero app change; the value comes from data already in
flight.

### Goals

- **Chart-configured mapping**: `tags:` in values maps a set of pod
  labels/annotations to tag keys (e.g. `team: metadata.labels["team"]`), applied
  by `k8sattributesprocessor`'s `extract.metadata`/`labels`/`annotations`.
- **Stable namespace**: mapped tags land as resource attributes under
  `avuru.tag.<key>` so they are unambiguous and never collide with semconv.
- **Cross-signal filtering**: a tag filter (`avuru.tag.team=payments`) applies to
  traces, logs, metrics, profiles and the service map — one filter vocabulary.
- **Discoverable**: the hub exposes the set of tag keys/values seen recently so
  the UI can offer them as filter chips (like existing attribute filters).
- **Bounded**: a capped number of mapped tag keys (cardinality guard), documented.

### Non-goals

- **Arbitrary post-hoc tagging rules in the UI** — mapping is chart/config-defined
  in v0.2 (it changes collection); UI-authored rules are a later item.
- **Tag-based RBAC / cost allocation math** — tags are for slicing, not billing.
- **Renaming/aliasing services** — this is additive metadata, not identity.
- **High-cardinality free-text annotations** — the cardinality guard rejects them.

## Solution

**Collection.** Extend the `k8sattributesprocessor` config (chart-rendered) to
extract the configured labels/annotations and rename them to `avuru.tag.<key>`.
Because k8sattributes already runs in the pipeline, this is a config change plus a
small rename step (a `transform` statement or the processor's own rename), not a
new component. Tags become **resource attributes**, so they persist on every
signal into the existing ClickHouse `ResourceAttributes` maps — **no schema
change**.

**Query.** The storage layer already filters on resource-attribute maps; add
`avuru.tag.*` to the filterable set in the trace/log/metric/profile queries and a
`GET /api/v1/tags` endpoint returning recently-seen tag keys → sample values
(bounded, from a cheap `groupArray(distinct …)` over a recent window).

**UI.** A tag filter control (chips) in the shared filter bar, wired the same way
as existing attribute filters, so it composes with service/time filters across
every view. The service-map and service-health group selectors gain "match by
tag".

```
pod labels/annotations ─▶ k8sattributes extract+rename ─▶ avuru.tag.* resource attr
                                                             │
              ClickHouse ResourceAttributes ◀───────────────┘
                    ▲                    └─▶ GET /tags (keys→values) ─▶ UI filter chips
      trace/log/metric/profile queries filter on avuru.tag.*
```

**Cardinality guard.** The chart caps the number of mapped keys and the docs warn
against mapping unbounded annotations; the `/tags` endpoint truncates value lists.
Upholds the storage discipline (bounded maps, no proprietary columns).

### Alternatives considered

- **A dedicated `tags` column/table** — breaks the "OTel attributes, no
  proprietary formats" locked decision; resource-attribute maps already carry it.
- **Tagging in the hub at query time from a live k8s watch** — puts the hub in a
  metadata-sync role and misses historical data; stamping at collection is
  durable and rides existing metadata.
- **UI-authored tag rules now** — more surface (validation, precedence, storage);
  chart-defined mapping matches how collection is configured today and ships first.

## What implementation changed

Three corrections, each because the codebase disagreed with the design.

**1. Collection happens at the sensor, not the gateway.** The AEP said the
mapping would be applied "in the gateway by the collector's
`k8sattributesprocessor` (already in the distro)". The gateway does not run that
processor and should not: it is central, may receive from several clusters, and
deliberately holds no cluster-wide RBAC. Enrichment happens where the metadata
is — node-local, in the sensor.

That splits the work in two, because the sensor's agent has no traces pipeline:

- **Logs and metrics** get their tags from `k8sattributes.extract.labels`.
- **Traces** get theirs from the eBPF tracer's own
  `attributes.kubernetes.resource_labels`, which maps any resource-attribute
  name to a list of pod labels.

Both write the same `avuru.tag.<key>` names, so one filter string reads either.
Worth noting the two neighbouring config keys behave *oppositely*: naming metric
features replaces the tracer's default list, while `resource_labels` merges into
its defaults — verified against v0.9.0, so the built-in
`service.name`/`namespace`/`version` resolution survives.

**2. Labels only; annotations are cut.** The trace path sources tags from pod
labels. An annotation-mapped tag would appear on logs and metrics and silently
vanish from traces — a filter vocabulary that means different things depending
on the screen is worse than one that does not exist. Annotations return when
the trace path can carry them.

**3. Tag filters land on traces and logs, not on every signal.** Metrics and
profiles have no attribute-filter UI to extend, and inventing one is a
different feature. The tags are *stamped* on metrics either way, so the query
side can follow whenever those screens grow filters.

`ServiceQuery`-shaped group selectors matching by tag are likewise deferred:
group resolution reads `ServiceLabel`, which carries namespace only, and
widening it is a service-health change rather than a tagging one.

### One vocabulary, one routing rule

A business tag describes the *workload*, so it lives in `ResourceAttributes`,
while an ordinary tag filter matches the record's own attributes
(`SpanAttributes` / `LogAttributes`). The storage layer routes on the reserved
prefix, so the API and the UI keep a single `tags=k=v,k2=v2` parameter and a
link carries between the traces and logs screens unchanged.

On traces the match is **participation-based** — the same rule the service
filter uses. A trace matches when any service that took part carries the tag,
not only when the root does; a root-only rule would hide every trace a tagged
service joined downstream, which is most of them.

## Verification

- **Unit**: k8sattributes rename config renders correctly; query builder accepts
  `avuru.tag.*` filters; `/tags` truncates to the cap.
- **Integration (ClickHouse)**: tagged telemetry is filterable by
  `avuru.tag.team` across traces, logs, metrics.
- **e2e (kind)**: label a demo workload `team=payments`, confirm its traces/logs
  carry `avuru.tag.team=payments` and the UI filter narrows to it; a
  service-health group selector matches by tag.
- **Wedge/TTV gate**: unchanged (no tags mapped by default).
- **Done** = an operator maps `team`/`tier` once in values and can slice every
  signal and group services by those tags, with a documented cardinality cap.

## Roadmap

- [x] AEP accepted
- [x] Chart `tags.labels` mapping → k8sattributes extract (logs, metrics) AND
      the tracer's `resource_labels` (traces), both writing `avuru.tag.*`
- [x] `avuru.tag.*` filters on traces (participation-based) and logs
- [x] `GET /api/v1/tags` (bounded keys→values discovery)
- [x] UI tag filter chips on the traces and logs screens
- [x] Cardinality cap (12 keys) and key-shape validation enforced at
      template time
- [ ] kind e2e: label a demo workload and assert its traces and logs carry the
      tag end to end
- [ ] docs-align (EN/FR)
- [ ] Deferred with reasons above: annotations as tag sources, metric/profile
      tag filters, group selectors matching by tag
