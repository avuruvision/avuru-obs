# deploy/helm — the Avuru Obs chart

`avuruobs/` is the vendor-neutral product chart: an OTLP backend on ClickHouse
that already-instrumented apps reach by pointing their exporter at the gateway.
One `helm install` brings up the gateway, a single-node ClickHouse, the hub API,
the UI, the sensor DaemonSet, and a schema-migration hook.

The chart is published to GHCR as an OCI artifact — install it by version, no
repo to add:

```bash
helm install avuruobs oci://ghcr.io/avuruvision/charts/avuruobs \
  --version <X.Y.Z> -n avuruobs --create-namespace
# point apps at:  http://avuruobs-gateway:4318   (or :4317 gRPC)
# UI:  kubectl -n avuruobs port-forward svc/avuruobs-ui 8080:80   (then http://localhost:8080/)
```

Contributors installing local changes use the chart in-tree instead:
`helm install avuruobs ./avuruobs -n avuruobs --create-namespace`.

## What it deploys

| Component | Kind | Notes |
|---|---|---|
| gateway | Deployment + Service | OTLP 4317/4318 → ClickHouse exporter (contrib collector + ConfigMap) |
| clickhouse | StatefulSet + Service + PVC | single-node default; skipped when `clickhouse.external.enabled` |
| hub | Deployment + Service | API only (the UI is its own deployable) |
| ui | Deployment + Service (+ Ingress) | static SPA, single-origin `/api` → hub |
| migrate | Job (Helm hook) | `hub migrate` on `post-install,post-upgrade` |

Every component can be switched off — `hub.enabled`, `ui.enabled`,
`gateway.enabled`, `sensor.enabled` — so one chart installs both a full
instance and a cluster that only ships telemetry to one (see
[Several clusters, one instance](#several-clusters-one-instance)).

No operator, no Zookeeper/Keeper — see the M2 design spec for the rationale.

## Key values

| Value | Default | Purpose |
|---|---|---|
| `image.registry` | `""` | Prefix every image (e.g. `harbor.example.com`) for a private registry |
| `hub.enabled` / `ui.enabled` / `gateway.enabled` / `sensor.enabled` | `true` | Install a subset. A secondary cluster runs gateway(+sensor) only; a query-only instance runs hub+UI only |
| `hub.external.url` | `""` | Where the hub is when this install does not run one — the gateway validates ingest keys against it |
| `clickhouse.external.enabled` | `false` | BYO ClickHouse — set `.address` + `.existingSecret` |
| `clickhouse.persistence.storageClassName` | `""` | `""` = cluster default StorageClass |
| `clickhouse.persistence.size` | `50Gi` | PVC size |
| `modules.logs.enabled` / `modules.infraMetrics.enabled` / `modules.profiling.enabled` / `modules.errorTracking.enabled` | `true` | Run a signal family, or not — one switch for schema + API + pipeline + collection + UI (see Modules) |
| `gateway.sentry.enabled` / `ingress.sentryHost` | `false` / `""` | Accept existing Sentry SDKs (needs the error-tracking + logs modules); give the ingest its own host |
| `gateway.receivers.jaeger.enabled` | `false` | Jaeger gRPC (`:14250`) + thrift-HTTP (`:14268`). UDP/thrift is deliberately not offered — no auth hook, and jaeger-agent is deprecated upstream |
| `gateway.receivers.zipkin.enabled` | `false` | Zipkin JSON/proto (`:9411`) |
| `gateway.receivers.prometheusRemoteWrite.enabled` | `false` | Prometheus remote-write **v2** (`:9291`); also needs `modules.infraMetrics`. A v1 sender is refused with `415` rather than dropped silently |
| `gateway.receivers.loki.enabled` | `false` | Loki push (`:3100`, Loki's own port so senders change hostname only); also needs `modules.logs` |
| `gateway.forward.otlp.enabled` / `.endpoint` | `false` / `""` | Dual-write to a second OTLP backend during a migration — `protocol` (`grpc`\|`http`), `insecure`, `headers`, `signals` |
| `gateway.forward.kafka.enabled` / `.brokers` | `false` / `[]` | Dual-write to Kafka — `topics` per signal, `encoding`, `tls`, and SASL **only** via `sasl.existingSecret` (keys `sasl-username` / `sasl-password`), never inline |
| `retention.traces` / `retention.logs` | `7` / `3` | Per-signal TTL in days |
| `ingress.enabled` / `ingress.host` | `false` / `avuruobs.local` | Expose the hub UI |
| `auth.enabled` | `true` | Login + per-project RBAC, secure by default (bootstrap `admin` password in the release Secret — see the install NOTES) |
| `auth.oidc.enabled` | `false` | Enterprise SSO via any OIDC IdP: set `issuer`/`clientId`/`publicUrl`, the client secret via `existingSecret` (key `oidc-client-secret`) or `clientSecret`, and IdP-group → role-on-projects `mapping` rules; `forceSSO` hides the local login form |
| `auth.trustedOrigins` | `[]` | Origins accepted by the CSRF check besides the request's own Host — the fix when a reverse proxy rewrites `Host` to the ingress address and the browser's correct `Origin` no longer matches (every write 403s, login included). Full origins: `["https://obs.example.com"]`; `auth.oidc.publicUrl` is trusted automatically |
| `auth.originCheck` | `enforce` | How a cross-origin write is treated: `enforce` (reject) \| `log` (allow, and log the `Origin`/`Host` pair — how you learn what the proxy sends) \| `off` (skip the check). Below `enforce` a CSRF defense is disarmed; prefer `auth.trustedOrigins` |
| `auth.ingest.mode` | `log` | Per-project ingest API keys, checked in the gateway: `off` \| `log` (validate + count, reject nothing — pipeline unchanged, so existing unkeyed senders keep working) \| `enforce` (reject unkeyed/invalid, and the key's project becomes the authoritative tenant) |
| `auth.ingest.provisionSensorKey` | `true` | Mint and seed a key for the sensor, so `enforce` never silences avuru's own agent |
| `gateway.tenant` | `""` | Tag ALL telemetry through this gateway with a project/environment (see Projects) |
| `projects` | `[]` | Declare the project list the hub advertises to the UI switcher |
| `sensor.priorityClass.create` | `false` | Run the sensor below default priority ("do no harm") |
| `sensor.collection.excludeNamespaces` | kube-system, kube-node-lease, kube-public | Namespaces never collected (any signal) |
| `sensor.collection.optOutLabel` | `avuru.obs/instrument` | Pods labeled `=false` are never traced; their logs/pod-metrics dropped |
| `sensor.collection.nodeOptOutLabel` | `avuru.obs/collect` | Nodes labeled `=false` get no sensor pod at all |
| `sensor.obi.discovery.mode` | `optOut` | `optIn` attaches uprobes ONLY to pods labeled `avuru.obs/instrument: "true"` (logs/metrics/inventory unaffected) |
| `sensor.obi.network.enabled` | `false` | Kernel L4 flow topology + per-edge connection health, without uprobes. Needs `modules.infraMetrics`; turns on `hostNetwork` for the sensor pod |
| `sensor.obi.network.interZone.enabled` | `false` | Bytes crossing an availability-zone boundary, per zone pair — the cross-AZ line on a cloud bill, measured in the kernel. Works **on its own**: it does not require `sensor.obi.network.enabled`, and its cardinality is zone pairs rather than workload pairs. Needs `modules.infraMetrics`, nodes labeled `topology.kubernetes.io/zone`, and (like the flow feature) `hostNetwork` |
| `tags.labels` | `{}` | Business tags: map a pod label onto every signal as `avuru.tag.<key>` (e.g. `team: team`), then filter traces and logs by it. Applied at collection, so uninstrumented workloads are tagged too. Capped at 12 keys — each one is a metric dimension |

## Modules (which signal families this install runs)

A module is the whole vertical for a signal family: turning one off skips its
ClickHouse schema, 404s its Hub API routes, drops its gateway pipeline, stops
its collection, and hides its UI entry. Everything is on by default, so an
existing install upgrades unchanged.

| Module | Value | Covers |
|---|---|---|
| core | *(no switch)* | Service map, traces, RED — the wedge, always on |
| logs | `modules.logs.enabled` | Log collection, search, and trace correlation |
| infra-metrics | `modules.infraMetrics.enabled` | Node/pod metrics, the sensor inventory, and any OTLP metrics you send |
| profiling | `modules.profiling.enabled` | Continuous CPU profiling and flame graphs |
| error-tracking | `modules.errorTracking.enabled` | Exceptions grouped into deduplicated, triageable issues (derived from your data; optional Sentry-SDK ingest) |

```bash
# Traces-only install: lighter ClickHouse, service map still live in <5 min.
helm upgrade --install avuruobs deploy/helm/avuruobs \
  --set modules.logs.enabled=false \
  --set modules.infraMetrics.enabled=false \
  --set modules.profiling.enabled=false
```

A module is the master switch over the collection knobs below: with
`modules.profiling.enabled=false`, `sensor.profiler.enabled=true` still
collects nothing. Turning a module on later is a values change plus
`helm upgrade` — the migrator is idempotent and applies the newly-active
schema. Turning one **off** leaves existing tables in place (no data is
dropped); delete them yourself if you want the space back.

## Authenticating ingest (per-project API keys)

Without keys, a gateway trusts whatever tenant a sender claims. Keys replace
that with a credential: the key's project *is* the tenant.

The rollout is deliberately three-staged so you never black-hole a sender you
forgot about. **Do not skip straight to `enforce`.**

```bash
# 1. Ship the default (mode: log). Nothing is rejected and the pipeline is
#    unchanged — this stage exists to find senders you don't know about.
helm upgrade avuruobs ... # auth.ingest.mode=log is the default

# 2. Mint a key per sender: UI → Settings → General → Ingest API keys, or
#    POST /api/v1/projects/{project}/keys. The secret is shown ONCE.
#    Put it in the sender's OTLP exporter:
#      OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer avuruk_..."

# 3. Watch the gateway's would-be-denial counter reach zero, then flip:
helm upgrade avuruobs ... --set auth.ingest.mode=enforce
```

A non-zero denial count in `log` mode is exactly the list of senders that would
break under `enforce`. The chart provisions the sensor's own key automatically
(`auth.ingest.provisionSensorKey`), so avuru's own agent is never the one that
breaks. Set `auth.ingest.mode=off` to remove the surface entirely.

## Upgrading

```bash
helm upgrade avuruobs oci://ghcr.io/avuruvision/charts/avuruobs \
  --version <new> -n avuruobs   # reuse your -f values / --set flags
```

Schema changes are applied by the `migrate` Job, a `post-install,post-upgrade`
Helm hook running `hub migrate`. It runs **after** Helm applies the manifests
(so the in-chart ClickHouse already exists) and records each applied version in
a `schema_migrations` ledger, so:

- **Migrations are additive and idempotent.** Every statement is
  `IF NOT EXISTS`; already-applied versions are skipped. Re-running, or enabling
  a module later, only applies what is newly due — never re-applies or drops.
- **They are forward-only** — there are no down-migrations. `helm rollback`
  reverts the manifests (images, config), not the schema. Rolling back to an
  older `appVersion` is safe when the newer version only *added* tables (an
  older hub ignores tables it doesn't know); it is **not** safe across a
  shape-changing migration. Roll forward; don't roll back across a schema
  change.
- **The Job is kept for inspection**, failed or not. `hook-delete-policy:
  before-hook-creation` plus `ttlSecondsAfterFinished: 3600` keeps it for an
  hour (`kubectl logs job/…-migrate`); it is recreated on the next upgrade.
  `--wait` surfaces a failure rather than leaving a half-migrated store.

**The hub also repairs the schema itself** (`hub.autoMigrate: true`). This is
not redundancy for its own sake: Helm runs `post-install`/`post-upgrade` hooks
only *after* `--wait` succeeds, so a release that times out waiting for any
component never creates the migrate Job — while the Deployments Helm already
applied keep rolling out. The result is a cluster that looks healthy and whose
every query fails on a missing table. When the hub finds its schema incomplete
it applies the same embedded migrations (idempotent, and safe to run
concurrently with the Job or another replica), and if it cannot, it logs one
`ERROR` naming the remedy instead of retrying silently. Settings → Status shows
a **Schema** component with the applied/expected count.

Set `hub.autoMigrate: false` when schema is owned elsewhere — a DBA-managed
external ClickHouse, or a query-only hub user without DDL rights. The Job then
remains the only mechanism.

```bash
# Did migrations ever run?
kubectl -n <ns> logs deploy/avuruobs-hub | grep 'clickhouse schema ready'
kubectl -n <ns> exec avuruobs-clickhouse-0 -- \
  clickhouse-client -u avuru --password <pw> -q "SELECT count() FROM otel.schema_migrations"
```

Because collection is driven by ConfigMaps with a checksum annotation, a
`helm upgrade` that changes sensor/gateway config rolls those pods
automatically — no manual restart.

## Deactivating collection (what turns off what)

Every knob is a Helm value (a `helm upgrade` rolls the DaemonSet via its
config checksum) — except the per-node label, which works instantly with no
upgrade. The platform's own namespace is always excluded.

| Scope | Traces/RED (OBI) | Logs (filelog) | Pod metrics (kubeletstats) | Profiles |
|---|---|---|---|---|
| Whole signal | `sensor.obi.enabled=false` | `sensor.agent.logs.enabled=false` | `sensor.agent.kubeletstats.enabled=false` | `sensor.profiler.enabled=false` |
| Namespace | `sensor.collection.excludeNamespaces` (+ `sensor.obi.discovery.excludeNamespaces` for traces only) | same shared list | same shared list (pod-scoped datapoints only; node metrics unaffected) | not supported — whole-node profiler |
| Pod / app | label the pod `avuru.obs/instrument: "false"` | same label | same label | not supported |
| Node (= agent instance) | `kubectl label node <n> avuru.obs/collect=false` — removes the entire sensor pod | same | same | same |

Cautious fleets can invert the traces column instead of opting out piecemeal:
`sensor.obi.discovery.mode=optIn` attaches uprobes **only** to pods labeled
`avuru.obs/instrument: "true"`, so tracing is adopted deliberately while logs,
metrics and the node inventory keep flowing. The staged path for enabling the
sensor on a running fleet is `docs/runbooks/sensor-rollout.md`.

Examples:

```bash
# Exempt a probe-sensitive workload from eBPF instrumentation + collection:
kubectl -n payments patch deploy checkout --type=merge \
  -p '{"spec":{"template":{"metadata":{"labels":{"avuru.obs/instrument":"false"}}}}}'

# Stop collecting an entire namespace:
helm upgrade avuruobs ./avuruobs --reuse-values \
  --set 'sensor.collection.excludeNamespaces={kube-system,kube-node-lease,kube-public,payments}'

# Pull the sensor off one node right now (no helm upgrade):
kubectl label node worker-3 avuru.obs/collect=false
```

If apps fail probes after installing the chart, start at
`docs/runbooks/app-probe-failures.md` — prefer these targeted opt-outs over
uninstalling.

## Sizing & overlays

The chart stays environment-agnostic; overlays carry the sizing. Three ship in
this directory — compose them left-to-right (`-f` wins rightward):

| Overlay | Shape | ClickHouse |
|---|---|---|
| `values-staging.yaml` | small — 1 replica each, short retention | in-chart, 20Gi |
| `values-prod.yaml` | medium — 2 replicas, `priorityClass`, ingress | in-chart, 100Gi |
| `values-external-clickhouse.yaml` | large / HA — 3 replicas, long retention | **external (BYO)** |

**High availability = external ClickHouse.** The in-tree ClickHouse is
single-node by design (no Keeper); for HA point `clickhouse.external.*` at an
operator-managed cluster and layer `values-external-clickhouse.yaml`. The
stateless tier (gateway, hub, ui) scales with plain `replicas`.

**Disk.** ClickHouse storage tracks `retention.<signal>` days × ingest rate
(compressed, and columnar compression is high). Start from the overlay sizes,
watch actual PVC/backend growth over one full retention window, and adjust —
raising retention raises disk roughly linearly. The sensor's own CPU/memory
scales with the number of processes per node, not with retention.

## Migrating from another backend (non-OTLP senders)

Every receiver below is off by default and adds a Service port; nothing else
about the install changes. Enabled receivers go through the **same** tenant
stage as OTLP, so `auth.ingest.mode: enforce` and per-project keys apply
identically whatever protocol the data arrived on — there is no side door.

The known limitation, stated once: a legacy sender that cannot set request
headers has nowhere to put its ingest key. Under `enforce` such a sender is
rejected. Give that environment its own gateway with `gateway.tenant` set, or
keep `auth.ingest.mode: log` for it.

```bash
# From Jaeger — collector-compatible ports; senders change the endpoint only.
helm upgrade avuruobs ... --reuse-values --set gateway.receivers.jaeger.enabled=true
#   jaeger-client gRPC   -> avuruobs-gateway:14250
#   thrift over HTTP     -> http://avuruobs-gateway:14268/api/traces

# From Zipkin / Brave / OpenCensus.
helm upgrade avuruobs ... --reuse-values --set gateway.receivers.zipkin.enabled=true
#   -> http://avuruobs-gateway:9411/api/v2/spans

# From Prometheus remote_write (v2 only; needs modules.infraMetrics).
helm upgrade avuruobs ... --reuse-values --set gateway.receivers.prometheusRemoteWrite.enabled=true
#   prometheus.yml:
#     remote_write:
#       - url: http://avuruobs-gateway:9291/api/v1/write
#         headers: { Authorization: "Bearer avuruk_..." }   # required under enforce

# From Loki / Promtail (needs modules.logs). Same port Loki uses, so the
# sender's hostname is the only edit.
helm upgrade avuruobs ... --reuse-values --set gateway.receivers.loki.enabled=true
#   -> http://avuruobs-gateway:3100/loki/api/v1/push
```

Loki stream labels land as log **record** attributes, not resource identity —
a pushed line is found by its body or attributes, not by the service filter,
which is backed by `ServiceName`.

Only Service ports are opened. The ingress rule routes `/api` to the hub, so
exposing a receiver path publicly needs its own host — the same pattern as
`ingress.sentryHost`; in-cluster senders need nothing.

### Dual-writing while you evaluate

Adopting a backend should be reversible, and evaluating one usually means
running both for a while:

```bash
helm upgrade avuruobs ... --reuse-values \
  --set gateway.forward.otlp.enabled=true \
  --set gateway.forward.otlp.endpoint=old-collector.observability:4317 \
  --set gateway.forward.otlp.insecure=true \
  --set "gateway.forward.otlp.signals={traces,logs}"
```

Forwarders always render with a bounded sending queue, so a legacy target
that goes down cannot backpressure the ClickHouse path — the failure mode
that makes dual-write untrustworthy. `enabled` with an empty
`endpoint`/`brokers` fails the render loudly rather than silently forwarding
nowhere.

Checking a protocol before you commit: `tools/compatsend` sends one real
fixture per protocol (`-proto jaeger|zipkin|promrw|promrw-v1|loki`, `-key` for
an ingest key) against any install — the same sender the compose
(`make e2e-compat`) and kind gates use.

## Several clusters, one instance

One instance, many clusters: each cluster runs the ingest half of the chart and
writes to the central ClickHouse under its own project. Nothing is federated —
there is one store, and a project is how a cluster's telemetry stays
identifiable inside it.

**Central cluster** — the full install, plus a project per cluster so the
switcher lists them before their first span arrives:

```bash
helm install avuruobs deploy/helm/avuruobs \
  --set projects='{prod-eu,prod-us}'
```

**Each secondary cluster** — gateway (+ sensor), pointed at the shared store:

```bash
helm install avuruobs deploy/helm/avuruobs \
  --set hub.enabled=false --set ui.enabled=false \
  --set clickhouse.external.enabled=true \
  --set clickhouse.external.address=clickhouse.central.example.com:9000 \
  --set clickhouse.external.existingSecret=clickhouse-password \
  --set gateway.tenant=prod-eu
```

`gateway.tenant` is what makes the telemetry identifiable: it stamps
`avuru.tenant` on everything passing through, and the central UI shows it under
that project. Give each cluster a different one.

With **ingest keys** on (`auth.ingest.mode` ≠ `off`), a secondary cluster also
needs to reach the hub that issues them, and to share its internal token:

```bash
  --set hub.external.url=https://avuruobs.example.com \
  --set auth.ingest.internalToken=<the central hub's token>
```

The token is the one in the central cluster's `<release>-ingest` Secret
(`internal-token`). The chart refuses the combination without it rather than
generating a local one the central hub has never seen — every validation would
fail closed, and the symptom (all telemetry rejected) would point at the wrong
thing.

What the secondary cluster does NOT get: the migrate Job (the schema belongs to
the central instance — two clusters migrating one database is a race), the auth
Secret, and any hub config. The chart refuses the combinations that cannot
work — `ui.enabled` without a hub, or a hub-less install writing to the
in-chart ClickHouse — at `helm template` time, with a sentence saying why.

A **query-only** instance is the mirror image, for a cluster that receives
telemetry from elsewhere and runs nothing of its own:

```bash
helm install avuruobs deploy/helm/avuruobs \
  --set gateway.enabled=false --set sensor.enabled=false
```

## Downstream consumption

This chart is the canonical artifact. An enterprise overlay (separate repo)
layers Harbor image refs, Kustomize patches, and Keycloak/oauth2-proxy via
`helm template ./avuruobs -f overlay-values.yaml | kustomize ...` — it never forks
the chart.

## Declared service metadata

A service can group and tier itself with three optional OTLP resource
attributes — no hub config needed:

| Attribute | Meaning |
|---|---|
| `service.namespace` | logical domain; becomes the group name (spans k8s namespaces) |
| `deployment.environment.name` | environment; splits a domain into per-env groups |
| `avuru.tier` | criticality `T0`–`T3` |

The environment falls back to the deprecated `deployment.environment` when the
current semconv key is absent, so SDKs emitting either are picked up.

Declaring nothing keeps the zero-config behavior: services auto-group by
Kubernetes namespace at `serviceGroups.defaultTier`.

Tier precedence, most specific first:
`serviceGroups.tierOverrides[<service>]` → a matched `serviceGroups.groups`
entry → the declared `avuru.tier` → `serviceGroups.defaultTier`. Where members
of one group declare different tiers, the **most critical** wins.

An invalid declared tier never fails the hub: the service falls back to the
default tier and the `/api/v1/health/groups` response carries a warning. This is
deliberately the opposite of `serviceGroups` config, where a bad tier fails the
hub loud — config is operator-reviewed, application telemetry is not.

A rule or budget naming a group covers **every** environment of that group
unless narrowed (`selector.environments` on an alerting rule).

## Verification

`make e2e-helm` (from repo root) spins a kind cluster, installs the chart,
seeds deterministic OTLP, and asserts traces + correlated logs via the hub API.
The same install enables all four compat receivers and OTLP forwarding, sends
one real fixture per protocol through the chart-rendered ports, asserts the
rows in ClickHouse, and greps a stand-in legacy backend's logs for the
forwarded trace — so the compatibility claim above is CI-enforced, not
asserted. `make e2e-compat` is the faster compose equivalent (opt-in), and
additionally covers enforce-mode rejection and the remote-write v1 `415`.

