# AEP: Transport classified from Kubernetes labels

- **Date:** 2026-08-26
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Let the service map recognise transport infrastructure from the **Kubernetes
labels the mesh writes**, not only from workload names. The sensor carries a
small, fixed set of mesh-identifying labels on the spans it already produces;
`ListServices` reports, per service, whether any of them was ever seen; and the
classifier treats that as **positive evidence only** — a label can promote a
workload to transport, never demote one. Names stay exactly as they are, and an
unlabelled cluster behaves byte-for-byte as it does today.

## Motivation

[The transport AEP](2026-08-23-service-map-transport.md) classifies by name and
says why its built-in list is deliberately narrow: *a false positive erases a
real service from the map*. That narrowness is the cost of guessing from a
string. It also leaves the opposite failure wide open — a gateway an operator
called `public-edge` is transport that the product cannot recognise, so its
hops are drawn as application dependencies until somebody notices and edits a
ConfigMap.

That AEP already named the fix and deferred it: *"Detect the mesh from
Kubernetes labels — better signal than names, and the right long-term answer,
but it needs attributes the pipeline does not carry today."* The pipeline now
carries exactly this shape of attribute: the business-tags work maps pod labels
onto spans through OBI's `resource_labels`, and the same mechanism can carry a
curated set that no operator should have to configure.

Ties to the [wedge](../AGENTS.md): a fresh cluster gets a correct map without
anyone declaring their gateway's name. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
no new collection path, no new table, the hub reads via SQL, and the
classification stays in the one package that owns it.

### Goals

- **Recognise standalone proxies by label**: Gateway API data planes, Istio
  gateways and waypoints, istiod, Linkerd control-plane components.
- **Strictly additive**: an install with none of these labels is unchanged, and
  the operator's `applications` escape hatch still wins over everything.
- **No new query**: the evidence rides the `ListServices` call the map already
  makes, so it cannot describe a different set of services than the map draws.

### Non-goals

- **Replacing name classification.** See the finding below — for a large part
  of the fleet there is no label to read.
- **Reading the Kubernetes API from the hub.** The hub is not a controller; the
  evidence arrives as telemetry, like everything else it knows.
- **Per-mesh feature detection.** This says "carries traffic", not "is Istio
  1.22 in ambient mode".
- **Letting a label mark something as an application.** Absence of a label is
  not evidence of absence, so the signal only points one way.

## Solution

### The finding that shapes it: a sidecar has no label of its own

A label is a property of a **pod**, and in the sidecar model the proxy is a
*container inside the application's pod*. `istio-proxy` appears on the map as
its own node because it emits spans under its own service name — but the pod
carrying it is the application's pod, wearing the application's labels. There
is no label that distinguishes the sidecar from the workload it is attached to,
and any attempt to read one would classify the application as transport and
erase it from the map: precisely the failure the parent AEP built its narrow
list to avoid.

So labels are decisive for **standalone** proxies — gateways, waypoints,
ztunnel, control planes, which are workloads in their own right — and silent
for sidecars. That is not a gap to be closed later; it is the shape of the
data. Names remain the answer for sidecars, and this AEP makes labels an
addition to them rather than a replacement. The roadmap's phrasing ("names are
a heuristic, labels are the real signal") is half right, and the half that is
wrong matters.

### The labels

Curated, fixed, and matched on **presence**, not value:

| Label | Written by |
|---|---|
| `gateway.networking.k8s.io/gateway-name` | any Gateway API implementation's managed data plane |
| `istio.io/gateway-name` | Istio gateways and waypoints |
| `operator.istio.io/component` | istiod and the components the operator manages |
| `linkerd.io/control-plane-component` | Linkerd control plane |
| `linkerd.io/extension` | Linkerd extensions (viz, jaeger) |

Deliberately excluded, and the reason is the same each time: they mark a
**meshed application**, not a proxy. `service.istio.io/canonical-name` and
`security.istio.io/tlsMode` are on every sidecar-injected workload in the
cluster; classifying on them would empty the map of applications.

### The path

The sensor maps each label onto a resource attribute under `avuru.transport.*`
via OBI's `resource_labels` — the same merge-into-defaults mechanism business
tags use, so the built-in `service.name` resolution is untouched. Traces only:
the classifier reads `otel_traces`, and keeping these off the metrics path
means they add no metric dimension to anything.

`ListServices` gains one aggregate column — "was any `avuru.transport.*`
attribute ever non-empty for this service" — so the evidence arrives with the
services it describes, in the query the map already runs. No new round trip,
and no way for the two to disagree.

`topology.New` takes the evidence set alongside the config. Precedence, highest
first:

1. `applications` — the operator's word, still final.
2. **Label evidence** — the mesh's own word about its own workload.
3. Name patterns — the guess.

A workload that no label reached is classified exactly as it is today.

### Alternatives considered

- **Read labels from the Kubernetes API in the hub.** Direct, and it would see
  sidecar pods too — but it makes the hub a cluster-scoped API client, which it
  has never been, and it breaks on the query-only install that watches a
  cluster it has no credentials for.
- **Let the operator map the labels through `tags.labels`.** Zero new code, and
  it puts the burden on exactly the person who did not know their gateway was
  mis-drawn. It also charges metric cardinality for a trace-side decision.
- **Extract the labels with `k8sattributes` on the agent instead of OBI.** That
  path decorates logs and metrics too, which is cardinality spent on a question
  only the trace tables are asked.
- **Classify by container name (`istio-proxy`) rather than by pod label** to
  reach sidecars. That is what the name list already does, under another name.

## Verification

- **Unit** (`hub/internal/topology`): evidence promotes an unrecognised name;
  `applications` still beats evidence; an empty evidence set reproduces today's
  classification exactly, asserted against the existing table so the two cannot
  drift.
- **ClickHouse integration**: a service whose spans carry the attribute is
  reported as evidenced, one whose spans do not is not, and the flag survives
  the aux-exclusion filter.
- **Handler**: a gateway named `public-edge` — matching no built-in pattern —
  is stamped `transport` when its spans carry the label, and `service` when
  they do not. This is the whole rider in one test.
- **Chart**: the `avuru.transport.*` mappings render into the OBI config
  unconditionally, parse as valid OBI config, and do not appear in the agent's
  `k8sattributes` (a trace-side decision must not become a metric dimension).

## Roadmap

- [x] AEP accepted
- [x] Sensor: the curated labels onto `avuru.transport.*`, traces only
- [x] Storage: evidence column on `ListServices`
- [x] `topology`: evidence as positive-only input, below `applications`
- [x] API: pass evidence into the classifier on every surface that builds one
- [x] Tick the parent AEP's roadmap box
