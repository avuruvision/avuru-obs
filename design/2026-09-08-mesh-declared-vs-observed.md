# AEP: What the mesh was told, and what it did

- **Date:** 2026-09-08
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

Put, on the same row, what a mesh's configuration *says* about a namespace, a
workload or an edge and what its data plane *did*. Two halves, one join:

- **What it did.** The sensor scrapes the proxies themselves — ztunnel, waypoints,
  gateways, sidecars — for the one account nothing else can give: whether each
  request and connection was mutual TLS or plaintext, which response flag a failure
  carried, how traffic split across versions, and how many workloads a ztunnel is
  actually carrying.
- **What it was told.** The `mesh-config` module reads pods, so a workload exists
  below the namespace: whether a sidecar is in the pod, whether the node agent
  captured it, which waypoint it is bound to, and which PeerAuthentication —
  mesh-wide, namespace, or selector — decides its mTLS mode. Eleven more checks
  follow, each aimed at breakage that emits nothing or looks safe.
- **The join**, in the API layer, produces a posture per workload: strict and
  encrypted; permissive with named plaintext callers; permissive and safe to
  tighten; declared strict and observed plaintext — the finding no configuration
  reader can produce on its own, and the reason both halves ship in one line.

This reverses one deferral: [the mesh, by role](./2026-09-06-mesh-by-role.md)
put the ztunnel scrape off "until the telemetry-only views are in front of an
operator". They are, and the two questions they cannot answer — is this traffic
actually encrypted, and is a proxy refusing requests on purpose — are answered
only by the proxies.

## Motivation

The mesh screen shipped in v0.14 knows a proxy's rate, errors, latency, bytes and
what it carries, and — with `mesh-config` — which namespaces are enrolled and how.
Three things it cannot say, and an operator asks all three the first week:

1. **Is the traffic encrypted?** The namespaces tab shows an mTLS *mode*. A mode
   is a claim. STRICT that is enforced and STRICT that is not — a workload that
   never enrolled, a selector that matches nothing, a DestinationRule disabling
   TLS underneath — look identical from configuration and from traces. Only the
   proxy that terminated the connection knows, and it says so in
   `connection_security_policy`.
2. **Why did the proxy refuse?** A tripped circuit breaker, an exhausted retry
   budget, a route to nowhere and an upstream timeout are four different 503s
   with four different fixes, and the span records one status code. Envoy's
   `response_flags` names each.
3. **Which workloads are actually in the mesh?** The module reads Deployments
   but not pods, and enrolment is a fact about a pod: a sidecar is a container in
   it, ambient capture is an annotation the node agent writes on it. A namespace
   labelled for ambient with a workload the node agent never captured is the
   most common ambient misconfiguration, and it produces no telemetry of its own
   — the case [reading the mesh's configuration](./2026-09-07-mesh-configuration.md)
   held two checks back for.

The wedge holds. Nothing here changes what a fresh cluster installs to get a map
in five minutes: the scrape is discovered from annotations the mesh already
writes and runs only under a module that is born off; the pod read is a wider
version of a grant that already exists, still read-only.

### Goals

- Observed mTLS share per destination workload and per edge, from the data plane's
  own report; plaintext callers named.
- A declared mTLS mode per workload with its source (mesh, namespace, or the
  selector-scoped policy the previous AEP skipped).
- A posture per workload joining the two, with findings where they disagree or
  where tightening is safe.
- A workload inventory: every workload the cluster runs, with injection and
  capture truth, waypoint binding, covering policies, and whether it had traffic.
- Response flags and per-version split on the proxy page; ztunnel's own count of
  the workloads it carries.
- The checks the previous AEP held back, and the ones a security posture needs.
- Honest absence at every step, and no change to what a default install collects.

### Non-goals

- **Per-cluster Envoy upstream statistics** (outlier ejections, pending overflow
  counters). A default Istio install does not expose them; the proxy page says
  which setting turns them on rather than rendering zeros.
- **Per-proxy effective configuration** (`config_dump`). Unchanged: a debugger's
  job, needs the proxy admin API.
- **Writing anything.** Read-only, forever.
- **Non-Istio data planes.** The series are Istio-shaped and the screen says so;
  the workload inventory is mesh-agnostic.
- **Posture history and alerting on posture.** A later proposal, once the
  verdicts have been trusted on real clusters.

## Solution

### What it did: a sensor-side scrape of the data plane

The sensor DaemonSet already runs a Prometheus receiver for Kepler. A second
receiver, `prometheus/mesh-dataplane`, discovers pods **on its own node** (a
field selector on `spec.nodeName`), keeps those whose container is `istio-proxy`
or `ztunnel`, and scrapes the port the mesh annotates (`:15020`). The keep-list
is eight series: the request counter, the four TCP counters, and three ztunnel
readings. Series reported from the *calling* side duplicate the ones the called
side reports, so they are kept only from gateways, which have no called side to
report for them.

Per-node is the correct shape here, and it inverts rather than contradicts the
argument that put the istiod scrape in the gateway: istiod is one Deployment and a
DaemonSet would multiply it by the node count; proxies are one per pod, and a node
scraping its own pods counts each once.

It is on by default **under the mesh module**. `mesh.controlPlane` is born off
because it needs an endpoint nobody can discover; this needs no value at all. The
module is the consent. A previously valid values combination never starts failing:
mesh on with infra-metrics off still renders, without the receiver, and the hub
says so.

The hub reads counters as **per-series deltas**, the idiom the green module
established, never as `sum(Value)` — a share computed from re-summed cumulative
counters is wrong in a way that looks plausible. Where both ends reported the
same edge, the destination's account wins, then the waypoint's, then the source's.

### What it was told: pods, workloads, and the mode that actually applies

`mesh-config` watches pods. They are the largest kind on any cluster, so they are
**projected on ingest** to a dozen fields (labels, four annotations, owner,
service account, node, container names, phase) before the informer stores them,
and capped separately from configuration objects. A truncated pod list does not
degrade the pod-dependent checks; it **disables** them and the response says
which were skipped, so an empty issues column can never be read as a clean bill.
The ClusterRole grows by one resource and no verb.

From pods, a workload inventory: pods grouped by owner (the Deployment resolved
from the ReplicaSet's template hash, without watching ReplicaSets), with
`Injected` (an `istio-proxy` container), `Captured` (the redirection annotation),
the effective data-plane mode, the waypoint that binds it under the mesh's own
precedence, and the policies whose selectors match it. Declared mTLS is resolved
selector policy first, then namespace, then mesh-wide — which repairs the
previous AEP's decision to skip selector-scoped PeerAuthentications, and gives
every namespace row a source for its mode instead of a bare value.

### The join

The two halves key differently: telemetry on the service name the sensor
derives, configuration on `namespace/workload`. The join key is
`(namespace, workload)` and the join lives in the API layer, as the previous AEP
required; the service name's trailing `.namespace`, when present, is stripped
there. One fold computes the posture, and every consumer — the Security tab, the
Workloads tab, the namespace counts — reads that fold, so two screens cannot
disagree about one workload.

| Posture | When | Finding |
|---|---|---|
| strict, all mutual TLS | declared STRICT, no plaintext observed | — |
| declared strict, observed plaintext | declared STRICT, plaintext observed | `MESH_MTLS_NOT_ENFORCED` — the policy is not applied to this workload |
| permissive, all mutual TLS | no strict policy, no plaintext observed | `MESH_MTLS_READY_TO_TIGHTEN` — STRICT would refuse nothing now talking |
| permissive, plaintext callers | plaintext observed | `MESH_PLAINTEXT_CALLERS` — names them |
| traffic not carried | ambient namespace, traffic in traces, nothing reported by the data plane | `MESH_TRAFFIC_UNCARRIED` |
| observed only / idle / unknown | configuration not read / nothing in the window / only `unknown` | — |

Both misses keep their meaning. Configuration with no observation is *observed
nothing in this window*. Observation with no configuration is *not found in this
cluster*. Neither is dropped.

### The checks

Every new check is justified the way the previous set was: it names breakage that
emits nothing, or a configuration that looks safe and is not.

| Code | Fires when |
|---|---|
| `MESH_WAYPOINT_MISSING` | a namespace, service or workload bound to a waypoint that is not deployed (declared in v0.14, never emitted) |
| `MESH_POLICY_NO_MATCH` | a policy's selector matches no pod (held back for lack of pods) |
| `MESH_AMBIENT_NOT_ENROLLED` | a workload labelled for ambient, running, and neither captured nor injected (held back; now a pure check) |
| `MESH_L7_WITHOUT_WAYPOINT` | an object that needs a waypoint — HTTP-level authorization, request authentication, HTTP routing, HTTP connection pools — in an ambient namespace with none |
| `MESH_DATAPLANE_CONFLICT` | a sidecar in an ambient namespace, a pod carrying both labels, or a waypoint binding on a workload that is not ambient |
| `MESH_SUBSET_MISSING` | a route to a subset no DestinationRule defines |
| `MESH_HOST_CONFLICT` | two mesh-bound VirtualServices for one host, or two DestinationRules claiming one host |
| `MESH_GATEWAY_NO_WORKLOAD` | a Gateway, waypoints included, that no running pod serves |
| `MESH_LISTENER_CONFLICT` | listeners that share a port and hostname with different protocols |
| `MESH_PRINCIPAL_UNKNOWN` | an authorization rule naming a service account no running workload uses |
| `MESH_MTLS_CONFLICT` | widened: the target's *effective* policy, selector-scoped included; and the reverse case, mutual TLS demanded of a workload that disables it |

Not adopted, on purpose: port-naming conventions (this product's spans come from
the kernel, not from the proxy's protocol sniffing), "no authorization policy
covers this workload" (an empty list on the workload, not a finding), and
stylistic checks. The check set stays small, and stays aimed.

### Alternatives considered

- **Scrape the data plane from the gateway.** One target per proxy pod, a
  cluster-wide pod watch, and a scrape fan-out that grows with the cluster, in the
  component whose pipeline has no per-node scoping. The exact opposite of the
  reason istiod is scraped there. Rejected.
- **Keep source-reported series everywhere and deduplicate in SQL.** Doubles the
  ingest to keep data the hub then discards. Rejected; gateways are the one source
  with no destination reporter, and they are the exception the chart keeps.
- **Use the mesh's Telemetry API to trim labels at the proxy.** Writes to the
  cluster. Rejected.
- **Born-off flag for the scrape.** It would make the mesh module's own screen say
  "we are not looking" on every install that turned the module on. The module is
  the consent. Rejected.
- **Watch ReplicaSets to resolve Deployments.** Another cluster-wide grant and up
  to ten objects per Deployment for one fact the template hash already encodes.
  Rejected; a Deployment that cannot be confirmed is reported as its ReplicaSet,
  which is honest.
- **Pod templates instead of pods.** Declared, not actual: a template says a
  sidecar should be injected; only the pod says it was. Rejected.

## Verification

- **Chart:** the receiver renders only under the mesh module with infra-metrics
  and the agent on; inside the sensor ConfigMap and never the gateway's; every
  previously valid combination still renders; an empty keep-list fails loudly.
- **Storage:** integration tests against ClickHouse — the destination's report
  beats the source's; a counter read 100 then 130 contributes 30; plaintext
  callers are listed; the three silences resolve to three states.
- **Reader:** pods are projected, never cached whole; pods truncate on their own
  and truncation is deterministic; per-kind sync times are exposed.
- **Validation:** table-driven, one case per branch, and for every pod-dependent
  check a "pods truncated → silent, and the response says so" case.
- **API:** every posture verdict; every absence state naming its own fix; the join
  in both directions of miss.
- **UI:** Playwright over stubbed responses for the Security and Workloads tabs,
  the proxy page's flags and versions, a waypoint's list of what it serves, and the
  lock marker on map edges.
- **Real cluster:** on an ambient install, each proxy pod appears once in `up`,
  ztunnel answers on `/metrics`, `connection_security_policy` is present on its
  connection series, waypoints report as `waypoint`. Istio versions checked are
  recorded here when the line is cut.

## Roadmap

- [x] AEP accepted
- [ ] Data-plane scrape in the sensor chart
- [ ] Pods in the configuration reader, projected and capped
- [ ] Storage reads: security, request breakdown, ztunnel health
- [ ] Workload and service inventory; effective mTLS with its source
- [ ] The eleven checks
- [ ] Workloads and waypoint routes; posture and security routes; the join
- [ ] Workloads tab, Security tab, proxy page breakdown, map lock marker, per-role graph styling
- [ ] Docs aligned
