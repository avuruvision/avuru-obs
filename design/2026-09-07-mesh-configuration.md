# AEP: Reading the mesh's configuration

- **Date:** 2026-09-07
- **Author(s):** Avuru Obs maintainers
- **Status:** Draft

## Summary

Give the hub a read-only view of the Kubernetes and Istio objects that define
the mesh, behind a module of its own, so the product can answer three questions
telemetry cannot: which namespaces are enrolled and how, what configuration
exists, and whether that configuration is coherent.

This reverses a non-goal. [Mesh-facing surfaces](./2026-08-25-mesh-surfaces.md)
ruled per-proxy configuration inspection out of scope; that judgement stands for
what it actually addressed — dumping one proxy's effective routes, which is a
debugger's job and needs the proxy admin API. This proposes something different
in kind: reading the *declared* configuration from the API server, which is
neither per-proxy nor a dump, and validating it against itself.

## Motivation

Everything avuru-obs knows about a mesh today it learned from traffic. That is a
strength — it is why the map needs no configuration and works in five minutes —
and it has one hard edge: **a workload with no traffic does not exist.**

Three questions follow from that edge, and none of them can be answered:

1. **Is this namespace actually in the mesh?** An ambient namespace is enrolled
   by a label. A namespace labelled and *not* working looks, through telemetry,
   exactly like a namespace nobody labelled — silence either way. The most
   common ambient misconfiguration is invisible to us.
2. **What is configured?** Gateways, routes, destination rules, authorization
   and mTLS policies are the mesh's actual behaviour. We infer their effects
   from traffic and can never show their cause.
3. **Is the configuration coherent?** A route whose `backendRef` names a Service
   that does not exist is silently dead. A `parentRef` naming a missing Gateway
   never attaches. These are not rare; they are what a mesh change gets wrong,
   and they produce *no traffic at all* — so the tool that only watches traffic
   is blindest exactly where the failure is.

### Goals

- Namespace inventory: dataplane mode, waypoint binding, mTLS mode, config
  issue count — including namespaces with no traffic.
- Workload and service inventory with mesh binding, including workloads that
  emit nothing.
- A configuration browser with per-object validation findings.
- Honest absence at every step: no permission, no CRDs, not in a cluster, and a
  cluster too large to snapshot are four different states with four fixes.
- Off by default, and no new permission on any install that does not ask.

### Non-goals

- **Writing anything.** Read-only, `get/list/watch`, forever. The collection
  applier's narrow write grant stays the only write path this product has.
- **Per-proxy effective config** (`config_dump`). Still out of scope, still a
  debugger's job, still needs the proxy admin API.
- **Being a linter for Istio.** The check set is deliberately small and aimed at
  breakage that produces silence. Style and best-practice checks are somebody
  else's product.
- **Non-Istio meshes.** The namespace and workload halves are mesh-agnostic; the
  config half is Istio- and Gateway-API-shaped and says so.

## Solution

### A separate module, `mesh-config`

Not an extension of `mesh`. The reason is the permission: `mesh` needs no
cluster access at all today, and folding a `ClusterRole` into it would silently
escalate every install that has already set `modules.mesh.enabled` — an upgrade
that quietly grants cluster-wide read is exactly the surprise an operator should
never get from a patch release.

It takes a hard dependency on `mesh`, enforced in `modules.Parse` the way Green
and Cost depend on `infra-metrics`, plus a matching chart `fail` guard.

### Hub-side informers, not a telemetry receiver

The sensor could ship config objects through the pipeline with a
`k8sobjectsreceiver` and no hub RBAC at all. Rejected, and the reason is the
shape of the question rather than the cost: validation is a *join* across
objects — this route against that Service, this policy against those workloads —
and config would land in an append-only columnar store where every read becomes
"latest version per object". ClickHouse is the right home for observations over
time and the wrong home for current state. It would also tie config freshness to
telemetry batching and config retention to telemetry retention, and neither
should be true.

So: a `dynamic.Interface` client with informers, in a new `hub/internal/meshconfig`.
The structural precedent is `collectionApplier` in `hub/cmd/hub/main.go` — a real
implementation gated on in-cluster plus module, a `NoopReader` otherwise, so the
hub still boots under compose, in tests, and on a laptop.

This does not violate "the hub is never in the telemetry byte-path". Configuration
is not telemetry.

**The informer cache is the store.** No migration, no table, no tenant column,
no retention interplay: config is small, low-churn, and cheap to rebuild from a
list on restart. `lastSyncedAt` per kind is exposed, so staleness is visible
rather than assumed.

Objects are stripped on ingest — `managedFields` and
`last-applied-configuration` — which are routinely larger than the object.

### Validation as a pure package

`hub/internal/meshconfig/validate` takes a snapshot struct and returns findings.
No SQL, no HTTP, no clock: the same shape as `hub/internal/topology`, and
testable the same way, one table-driven case per check.

Findings carry our own codes. The initial set, each aimed at silence:

| Code | Fires when |
|---|---|
| `MESH_ROUTE_BACKEND_MISSING` | a `backendRef` names a Service that does not exist, or a port it does not expose |
| `MESH_ROUTE_PARENT_MISSING` | a `parentRef` names a Gateway that does not exist or has not accepted it |
| `MESH_GATEWAY_NO_ROUTES` | a Gateway listener nothing attaches to |
| `MESH_HOST_UNRESOLVED` | a VirtualService or DestinationRule host matching no Service or ServiceEntry |
| `MESH_WAYPOINT_MISSING` | `istio.io/use-waypoint` naming a waypoint absent from the referenced namespace |
| `MESH_AMBIENT_NOT_ENROLLED` | a namespace labelled for ambient whose workloads never appear behind ztunnel |
| `MESH_MTLS_CONFLICT` | a DestinationRule disabling TLS under a strict PeerAuthentication |
| `MESH_POLICY_NO_MATCH` | an AuthorizationPolicy or Sidecar whose selector matches nothing |

### Joining configuration to telemetry

The halves key differently: telemetry on `ServiceName` and resource attributes,
config on `kind/namespace/name`. The join key is `(namespace, workloadName)` and
the join happens in the **API layer**, where `stampServiceNamespaces` already
does the same job — never in the storage or config packages, which must each
stay answerable on their own.

Both misses are real and must read differently. Config with no telemetry is
**"no traffic in this window"** — the whole point of this work, a fact telemetry
alone can never state. Telemetry with no config is **"not found in this
cluster"**: another cluster, another tenant, or deleted mid-window. Neither is
silently dropped.

### Honest absence

`meshUnavailableReason` is the model — three silences, three fixes, each naming
the thing to change. The same discipline, five states:

| Situation | What the screen says |
|---|---|
| Module on, RBAC not granted | names the missing ClusterRole and the Helm value |
| Istio CRDs absent | per-kind availability; the rest of the screen still works |
| Hub outside a cluster | unconfigured, and the hub boots normally |
| Snapshot truncated | says so; never a silently short list |
| Informer stalled | `lastSyncedAt` per kind, on screen |

### Alternatives considered

- **Sensor-side `k8sobjectsreceiver`.** No hub RBAC, and that is its only
  advantage. Wrong store shape for a join, wrong freshness, wrong retention
  coupling. Rejected above.
- **Fold into the `mesh` module.** One switch instead of two, at the price of
  silently escalating every existing install on upgrade. Rejected.
- **A ClickHouse table for config.** Survives restarts without a re-list, and
  buys history. Costs a migration, dedup on every read, and a retention policy
  for state that is not observations. Rejected for v1; revisit if config
  *history* is ever asked for, which is a different feature.
- **Typed istio/gateway-api client-go modules** instead of the dynamic client.
  Nicer to write against; two more large dependencies and a version matrix
  against whatever Istio the operator runs. Rejected: unstructured reads are
  version-tolerant, which matters more here than ergonomics.

## Verification

- **Unit:** every check, table-driven, against a fake snapshot; the join in both
  directions of miss; `NoopReader` on a hub outside a cluster.
- **Handler:** routes absent with the module off; each absence state naming its
  own fix; a namespace with config and no traffic still listed.
- **Chart:** the ClusterRole renders only with the module on; the `mesh`
  dependency guard fires; nothing renders by default.
- **Playwright:** the namespace list, a config object with findings, and the
  no-permission state rendering as an instruction rather than an empty table.

## Roadmap

- [ ] AEP accepted
- [ ] `mesh-config` module + ClusterRole + `NoopReader` + honest absence
- [ ] Informers + snapshot, no screens
- [ ] Namespace and workload inventory, joined to telemetry
- [ ] Configuration browser
- [ ] Validation engine + findings surfaced per object and per namespace
