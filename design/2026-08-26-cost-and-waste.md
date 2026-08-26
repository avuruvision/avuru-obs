# AEP: Cost and waste — reserved versus used

- **Date:** 2026-08-26
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Add a **cost** module that reports, per workload and per node, the capacity
that was **reserved** against the capacity that was **used** — and ranks the
gap. Collection is the `k8s_cluster` receiver added to the sensor DaemonSet's
existing collector, coordinated by the `k8s_leader_elector` extension so that
exactly one node reports cluster-wide objects. Prices, if the operator supplies
them, are chart values; there is no pricing API and no outbound call. On an
install also running **green**, the same reserved-and-idle capacity carries an
energy and carbon figure, because it is the same waste measured twice.

## Motivation

Every release so far has answered *what is happening*. The question a platform
team gets from outside the platform team is *what is this costing* — and the
sharper version, *how much of it is buying nothing*. Today avuru-obs can answer
neither: `kubeletstats` gives node and pod **usage**, and nothing in the product
knows what any of it was **reserved** for. The Nodes screen shows a cluster
working; it cannot show a cluster half-empty and fully paid for.

This is the closest neighbour of the green module, which already measures the
energy a workload *draws*. Reserved-and-idle capacity is the same story in a
different unit: a request nobody used still pins a scheduler, still keeps a node
awake, still appears on an invoice.

Ties to the [wedge](../AGENTS.md): no application changes, no new agent, and the
receiver is already inside the image the sensor runs. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
reuse over rewrite (a pinned upstream receiver, no bespoke controller), OTLP into
the existing `otel_metrics_*` tables, the hub reads via SQL, the module framework
gates the surface — and, above all, **nothing leaves the cluster**.

### Goals

- **Per-workload waste**: CPU and memory reserved versus used over a window,
  ranked by the gap, with the workloads that reserve nothing called out rather
  than omitted.
- **Per-node allocation**: allocatable versus the sum of what is requested on it
  versus what is actually used — the difference between a full node and a busy
  one.
- **Optional money**: chart-declared rates turn cores and bytes into a currency.
  With no rates set the surface stays unit-less and still useful.
- **Green join**: on an install running the green module, reserved-and-idle
  capacity also reads in Wh and gCO2e.

### Non-goals

- **A pricing API.** No cloud billing integration, no instance-type price
  lookup, no phoning home. An operator who knows their rates writes them down.
- **Chargeback/showback accounting.** This is an engineering signal, not an
  invoice. No allocation of shared cost, no amortisation, no reserved-instance
  modelling.
- **Recommendations that act.** The product says what is over-reserved. It does
  not write a right-sizing patch, and nothing here touches a workload.
- **Storage or network cost.** CPU and memory only, which is where the reserved
  and used numbers both exist.

## Solution

### Collection — one receiver, one leader

The sensor DaemonSet already runs the stock
`otel/opentelemetry-collector-contrib:0.154.0` image, which carries both
components this needs. Two additions to the node collector's config:

```yaml
extensions:
  k8s_leader_elector:
    auth_type: serviceAccount
    lease_name: <release>-avuruobs-cluster
    lease_namespace: <release namespace>
receivers:
  k8s_cluster:
    auth_type: serviceAccount
    collection_interval: 30s
    allocatable_types_to_report: [cpu, memory]
    k8s_leader_elector: k8s_leader_elector
```

`k8s.container.cpu_request` / `cpu_limit` / `memory_request` / `memory_limit`
are **enabled by default** and are absolute gauges;
`k8s.node.allocatable_cpu` / `allocatable_memory` come from
`allocatable_types_to_report`, which is empty by default. They join the existing
OTLP pipeline to the gateway and land in `otel_metrics_gauge` like everything
else.

**The single-writer problem, and why there is no new workload.** Cluster objects
are cluster-scoped: a DaemonSet reporting them would multiply every series by
node count, which is the same trap the v0.9
[mesh AEP](2026-08-25-mesh-surfaces.md) hit with istiod and answered by moving
the scrape to the gateway. Here upstream provides the better answer directly —
`k8s_leader_elector` holds a `coordination.k8s.io` Lease and starts the receiver
on the holder only. The gateway stays off it deliberately: it is the ingest
front door, and cluster-wide object *reads* do not belong on it.

**RBAC.** The sensor ClusterRole already reads `pods`, `services`, `nodes`,
`deployments` and `replicasets`. The module adds the Lease verbs the extension
needs and the remaining object types the receiver watches, both rendered **only
when the module is on** — an install that does not run it gains no permission it
did not have.

### The numbers

Reserved per pod is the sum of its containers' requests; the pod→workload map is
the one `ServiceEnergy` and `ListPodStats` already share (`workloadExpr`, owner
precedence deployment > statefulset > daemonset). Used is the
`k8s.pod.cpu.usage` / memory series the infra-metrics module already stores. So
the join is between two things already in the same tables, and the module owns
**no schema and no migration** — the same shape as green and mesh.

Three honesty rails, because each of them is a way to be quietly wrong:

- **A workload that requests nothing is the loudest finding, not a blank row.**
  It is unschedulable-by-accident, unevictable-by-accident, and invisible to
  every quota. It gets its own state, not an empty cell.
- **Coverage is reported.** Requests are read from live objects; usage is a
  time series. A pod that existed for part of the window, or a container the
  receiver never saw, is named in an uncovered bucket rather than silently
  averaged away — the pattern green established with unattributed energy.
- **Limits are shown beside requests, never instead of them.** Reserved capacity
  is the *request*: it is what the scheduler subtracts from a node. A limit is a
  ceiling, and reading one as the other overstates waste on every burstable
  workload in the cluster.

### Money

`cost.rates.cpuCoreHour`, `cost.rates.memGiBHour` and `cost.rates.currency` are
chart values, unset by default. Set, they multiply; unset, the UI shows cores
and bytes and says so. A per-node-class rate table is a follow-up, not this AEP.

### Module and surface

A new `cost` module, **born OFF**, like green and mesh: it adds cluster-object
watching and a Lease that the install did not have before, and a default-on
module would switch that on for every existing install at upgrade — which is
exactly the reasoning green already carries. It **requires infra-metrics** (the
usage half of every number it prints), enforced by a chart guard in the style of
the existing green and mesh guards.

UI: a screen under **Infrastructure**, beside Nodes and Green, listing workloads
ranked by reserved-and-unused; the Nodes screen gains allocation beside
utilisation, because "89% allocated, 12% used" is one sentence and two very
different numbers.

### Alternatives considered

- **`kubeletstats`' optional `*_request_utilization` / `*_limit_utilization`
  metrics.** Tempting: one config block on a receiver already running, no leader
  election, no new RBAC beyond `nodes/pods`. Rejected on three counts. They are
  **ratios**, so recovering the absolute reservation means dividing usage by the
  ratio — undefined exactly when a workload is idle, which is precisely the
  workload the screen exists to find. The pod-level metric is **not emitted at
  all if any container in the pod lacks a request**, which silently deletes the
  worst offenders. And there is **no node allocatable metric**, so half the
  release is unbuildable from it.
- **A dedicated single-replica cluster-collector Deployment.** The conventional
  shape, and it keeps cluster reads off both the ingest path and the DaemonSet.
  Rejected because it buys a workload, a ServiceAccount and a failure mode to
  solve a problem an upstream extension already solves inside a process that is
  running anyway.
- **`k8s_cluster` in the gateway, next to the istiod scrape.** Consistent with
  v0.9 at first glance, and wrong on two counts: `gateway.replicas` is an
  operator value, so the duplication returns the moment anyone scales ingest;
  and it grants cluster-wide object reads to the component holding the open
  network port.
- **A cloud pricing API.** Would produce better numbers and would be the first
  outbound call in a product whose promise is that nothing leaves the cluster.
  Not a trade this product makes.

## Verification

- **Unit**: the reserved/used/gap arithmetic, the requests-nothing state, and
  the coverage accounting, including a window where a pod appears halfway
  through.
- **ClickHouse integration**: the container-request → pod → workload rollup
  against synthetic rows, with a pod whose containers disagree and a pod with no
  requests at all.
- **Chart templates**: the receiver, the extension and the Lease RBAC render
  **together or not at all** — the v0.9 sensor crash came from a switch and its
  prerequisite travelling separately — and no rule is granted with the module
  off.
- **kind e2e — the decisive gate.** The cluster runs more than one node, so
  leader election is actually under test rather than assumed. Assert
  `k8s.container.cpu_request` rows exist, and that **no series is reported
  twice**: grouping by object identity and `TimeUnix`, the maximum group size
  must be exactly 1. A regression in leader election shows up as 2, not as a
  slightly wrong number nobody notices.
- **Playwright**: the ranked list renders, a requests-nothing workload is
  visible as such, and with no rates configured no currency appears anywhere.

## Roadmap

- [x] AEP accepted
- [x] `k8s_cluster` + `k8s_leader_elector` in the sensor config, behind the module
- [x] RBAC (Leases + watched object types), rendered with the module only
- [x] Storage: reserved/used/gap per workload and per node
- [x] Hub API + `cost` module registration
- [x] Rates as chart values, absent by default
- [x] UI: the waste screen, and allocation on Nodes
- [ ] Green join: reserved-and-idle in Wh / gCO2e
- [x] kind e2e: no series reported twice
