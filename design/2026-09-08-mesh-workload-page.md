# AEP: The workload's page — what the cluster records, and what it logged

- **Date:** 2026-09-08
- **Author(s):** Avuru Obs maintainers
- **Status:** Accepted

## Summary

Make a workload's page on the mesh screen read like the cluster's own record
of it, and put its logs on it — its own, and the lines the node proxy and its
waypoint wrote about it.

- **The record.** The page already knew a workload's mode, its pods' count and
  its policies. It now carries what an operator reaches for first: when it was
  created, its type, its `app` and `version`, every label and the controller's
  annotations, each pod with the rollout it belongs to, a one-word health
  verdict with the reason that decided it, and — beside the policies that select
  it — the routes and rules that *reach* it through its Services.
- **The logs.** One tab, one stream, three sources: the workload's own log
  lines, the ztunnel access lines that name one of its pods, and the waypoint's
  access lines that name its Service. Searchable and filterable by severity,
  with a source toggle, under one cursor.

This reverses one choice the previous line made on purpose: the workload page
linked to the service screens "rather than rebuilding them". The link stays.
What it could not do is show the proxies' account of the workload — ztunnel and
waypoint lines are filed under *their* service names, and no other screen joins
them back to the workload they are about.

## Motivation

On an ambient cluster with Kiali beside it, the first things a reader asks of
a workload are exactly the things the page did not say:

1. **What is this, and since when?** Type, creation date, app and version,
   labels, annotations. The reader keeps the cluster's record in another tab.
2. **Which pods, and which rollout?** Two pods on two revisions during a bad
   deploy is the shape of most incidents, and "2 of 2 running" hides it.
3. **What configuration names this workload?** Policies select by label; routes
   and rules reach by Service. A page that lists only the first calls a routed
   workload unconfigured.
4. **What did the proxies see it do?** The application logs one side of every
   request. The node proxy logs the connection — source, destination, bytes,
   duration, identity — and the waypoint logs the HTTP exchange. All three are
   already in the store; none were on one screen.

The wedge holds: nothing here adds a permission, a scrape or a schema. The
record comes from objects the `mesh-config` reader already watches, and the
logs from the tables the logs module already fills.

### Goals

- A workload's page carries the fields above, from the snapshot, with the
  bounds stated below.
- Routes and rules are attached to workloads through the Services that select
  them, using the same host resolution the checks use.
- A logs route per workload composes its sources server-side and pages them
  under one keyset cursor.
- Every absence is stated: a controller not read, a pod list cut, a waypoint
  not bound, `mesh-config` off.

### Non-goals

- Pod readiness, restarts and container states: the reader projects a dozen
  fields per pod on purpose, and these are not among them.
- Editing anything. The page reads.
- Log lines from sidecars (`istio-proxy` in the pod): they share the pod's
  service name and are already the workload's own lines.
- A second log renderer. The tab uses the one the logs screen uses.

## Solution

### The record

`meshconfig.Pod` gains `CreatedAt` (already projected, now read).
`meshconfig.Object` gains `CreatedAt` for every kind and `Annotations` for the
three workload kinds only — Deployment, StatefulSet, DaemonSet — kept by
`keepObjectAnnotations` within bounds: 64 keys by name order, values cut at
2 048 bytes, `last-applied-configuration` never. `AnnotationsCut` says when the
bounds cut. `WorkloadsFrom` copies the controller's date and annotations onto
its workload; when no controller object was read, the oldest pod dates it and
`CreatedFrom` says `pods`.

The API's workload row gains `createdAt`, `createdFrom`, `app` (label `app`,
then `app.kubernetes.io/name`) and `version` (`version`, then
`app.kubernetes.io/version`). The page response gains `labels` (without
`pod-template-hash`), `annotations`, `annotationsCut`, `health` and `routes`;
each pod gains `revision` and `createdAt`.

**Health** is a fold in the API layer over the pods and the traffic row, in
the UI's `HealthStatus` vocabulary and with Kiali's thresholds so the two tools
agree: no pods → idle; none running → down; a fifth of requests failing → down;
a pod short or one in a thousand failing → degraded; running and silent → idle;
otherwise healthy. The reason names the trigger.

### Routes through Services

`attachRoutes`, run by `Validate` after the index is built, walks the objects
and attaches a `RouteRef` to every workload behind a resolved host: Gateway API
routes by `backendRefs` and by a Service `parentRef`; VirtualServices by `hosts`
and by `destination.host`; DestinationRules by `host`. Hosts resolve through
the index's `hostKey`, so a bare name means the object's own namespace and a
FQDN means exactly that Service. One reference per (object, Service), capped at
200 per workload. The page serves each with the object's own findings, exactly
as it serves the policies.

### The logs

`GET /api/v1/mesh/workloads/{namespace}/{name}/logs`, registered under the
mesh module when the logs module is also on. Params: the log screen's
`start`, `end`, `limit`, `cursor`, `q`, `severity`, plus `source` (comma list of
`app`, `ztunnel`, `waypoint`; default all) and, for the degraded case, an
optional `waypoint=namespace/name`.

The handler composes `storage.LogSource`s, a new field on `LogQuery`: each is a
set of service names and a conjunction of needle lists a line must contain.
The store renders them as one `WHERE` with OR groups —
`(ServiceName IN (…) AND multiSearchAny(Body, […]))` per source — so the
ordering and the `(Timestamp, TraceId, SpanId)` cursor are the ones every
other log read uses. The user's `q` stays `positionCaseInsensitive`; the
needles are exact lowercase names and take the cheaper function.

Sources:

- **app**: the sensor's two spellings of the service, `name` and
  `name.namespace`. No body needle.
- **ztunnel**: `ServiceName` `ztunnel`; needles are every pod of the workload
  by name (from the snapshot, through `podBelongsTo`, all of them and not the
  page's 50) plus `name.namespace.svc`.
- **waypoint**: the bound waypoint's name and `name.namespace`; same needles.

**Degradation ladder.** When `mesh-config` is off, the snapshot is not `ok`,
pods were not readable, the pod list was cut, or the workload is not in the
snapshot, the handler cannot name the pods. It then matches on two
conjunctions — `name.namespace.svc` or `workload="name-` together with
`namespace="namespace"` — which keeps `shop/checkout` apart from
`shop-staging/checkout`, and takes the waypoint only from the `waypoint=`
param. The response says so in `sources.precise` and `sources.fallback`, so an
empty ztunnel column is never silent.

The response is the logs screen's own shape (`logs`, `nextCursor`) plus a
`sources` descriptor, so the UI's `LogTable` renders it unchanged.

### The page

Two tabs, URL-keyed as `wltab`, `wlq`, `wlsev`, `wlsrc` — the mesh screen's
own keys (`q`, `ns`, `role`, `mode`, `wlns`) stay untouched. Overview: the
identity card, declared-versus-observed, the record, labels, annotations, pods,
and one "Istio config" section listing policies (with scope) and routes (with
the Service and host they came through), every reference linking into the
configuration browser. Logs: a small toolbar of its own — search, minimum
severity, three source toggles — a line naming the sources actually queried,
and the shared log table with a load-more sentinel. The Logs tab exists only
when the logs module is on.

### Alternatives considered

- **Extend `/api/v1/logs` with multi-service and body filters** — the join
  that makes ztunnel lines precise (pod names) lives in the hub's snapshot; a
  generic route would push cluster knowledge into the browser.
- **Three client-side streams merged in the UI** — three cursors cannot be
  merged into one stable one; pagination would repeat or skip lines.
- **Annotations on every object kind** — nothing reads a route's annotations,
  and the object list is capped for a reason.
- **Case-insensitive needles** — pod and Service names are lowercase by the
  API server's own rule; the cheaper function is correct.

## Verification

- **Reader:** the controller's date and annotations land on its workload; a
  ReplicaSet-owned pod falls back to the oldest pod; annotation bounds cut by
  name and by size and say so; `toObject` keeps annotations for the workload
  kinds only.
- **Routes:** backendRef, Service parentRef, VirtualService host and
  destination, DestinationRule host; a bare host resolves in its own namespace
  and a FQDN elsewhere; a non-Service backendRef and an unknown host attach
  nothing; one object naming one host twice is one reference.
- **API:** the page carries labels without the hash, annotations, routes with
  their findings, pods with revisions, a dated row; the list carries app and
  version and nothing heavier; the health fold, one case per verdict.
- **Logs route:** 404 without either module; three sources for a workload
  with a waypoint, two without; pod names and `name.ns.svc` as needles;
  `source=` narrows and rejects the unknown; `q`, `severity` and `cursor`
  reach the store; every rung of the degradation ladder names itself.
- **Store:** the rendered SQL — one OR branch per source, no `multiSearchAny`
  for a source without needles, the plain `ServiceName = ?` when no sources
  are set.
- **UI:** Playwright over stubbed responses — the overview's record, pods and
  configuration; the logs tab merging three services; filters travelling to
  the URL and to the hub; no Logs tab without the logs module.
- **Real cluster:** on the ambient install, `avuru-services/avuru-orchestrator`
  shows its labels, two pods on one revision, a health verdict, the waypoint
  link; its logs tab shows ztunnel lines naming its pods and waypoint lines
  naming `avuru-orchestrator.avuru-services.svc`.

## Roadmap

- [x] AEP accepted
- [x] Reader: creation time, bounded annotations, routes through Services
- [x] API: the record, health, routes with findings
- [x] Store: composed log sources under one cursor; the workload logs route
- [x] UI: Overview tab
- [x] UI: Logs tab
- [x] Docs aligned
