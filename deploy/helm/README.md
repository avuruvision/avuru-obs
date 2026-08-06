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

No operator, no Zookeeper/Keeper — see the M2 design spec for the rationale.

## Key values

| Value | Default | Purpose |
|---|---|---|
| `image.registry` | `""` | Prefix every image (e.g. `harbor.example.com`) for a private registry |
| `clickhouse.external.enabled` | `false` | BYO ClickHouse — set `.address` + `.existingSecret` |
| `clickhouse.persistence.storageClassName` | `""` | `""` = cluster default StorageClass |
| `clickhouse.persistence.size` | `50Gi` | PVC size |
| `modules.logs.enabled` / `modules.infraMetrics.enabled` / `modules.profiling.enabled` / `modules.errorTracking.enabled` | `true` | Run a signal family, or not — one switch for schema + API + pipeline + collection + UI (see Modules) |
| `gateway.sentry.enabled` / `ingress.sentryHost` | `false` / `""` | Accept existing Sentry SDKs (needs the error-tracking + logs modules); give the ingest its own host |
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
- **A failed migration is kept for inspection.** The hook is
  `hook-delete-policy: before-hook-creation,hook-succeeded` with
  `backoffLimit: 6`, so a failed Job stays around (`kubectl logs job/…-migrate`)
  and is recreated on the next upgrade. `--wait` surfaces the failure rather
  than leaving a half-migrated store.

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

## Downstream consumption

This chart is the canonical artifact. An enterprise overlay (separate repo)
layers Harbor image refs, Kustomize patches, and Keycloak/oauth2-proxy via
`helm template ./avuruobs -f overlay-values.yaml | kustomize ...` — it never forks
the chart.

## Verification

`make e2e-helm` (from repo root) spins a kind cluster, installs the chart,
seeds deterministic OTLP, and asserts traces + correlated logs via the hub API.
