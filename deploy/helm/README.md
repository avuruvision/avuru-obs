# deploy/helm — the Avuru Obs chart

`avuruops/` is the vendor-neutral product chart: a deployable OTLP backend
(traces + logs) that replaces Jaeger for OTLP-exporting apps. One
`helm install` brings up the gateway, a single-node ClickHouse, the hub
(API + UI), and a schema-migration hook. (The M4 operator + sensor DaemonSet
for the service map build on top of this chart.)

```bash
helm install avuruops ./avuruops -n avuruops --create-namespace
# point apps at:  http://avuruops-gateway:4318   (or :4317 gRPC)
# UI:  kubectl -n avuruops port-forward svc/avuruops-hub 8080:80
```

## What it deploys

| Component | Kind | Notes |
|---|---|---|
| gateway | Deployment + Service | OTLP 4317/4318 → ClickHouse exporter (contrib collector + ConfigMap) |
| clickhouse | StatefulSet + Service + PVC | single-node default; skipped when `clickhouse.external.enabled` |
| hub | Deployment + Service (+ Ingress) | API + embedded UI |
| migrate | Job (Helm hook) | `hub migrate` on `post-install,pre-upgrade` |

No operator, no Zookeeper/Keeper — see the M2 design spec for the rationale.

## Key values

| Value | Default | Purpose |
|---|---|---|
| `image.registry` | `""` | Prefix every image (e.g. `harbor.example.com`) for a private registry |
| `clickhouse.external.enabled` | `false` | BYO ClickHouse — set `.address` + `.existingSecret` |
| `clickhouse.persistence.storageClassName` | `""` | `""` = cluster default StorageClass |
| `clickhouse.persistence.size` | `50Gi` | PVC size |
| `retention.traces` / `retention.logs` | `7` / `3` | Per-signal TTL in days |
| `ingress.enabled` / `ingress.host` | `false` / `avuruops.local` | Expose the hub UI |
| `auth.enabled` | `false` | Forward placeholder — enforce auth at your ingress (OIDC is v0.2) |
| `gateway.tenant` | `""` | Tag ALL telemetry through this gateway with a project/environment (see Projects) |
| `projects` | `[]` | Declare the project list the hub advertises to the UI switcher |
| `sensor.priorityClass.create` | `false` | Run the sensor below default priority ("do no harm") |
| `sensor.collection.excludeNamespaces` | kube-system, kube-node-lease, kube-public | Namespaces never collected (any signal) |
| `sensor.collection.optOutLabel` | `avuru.obs/instrument` | Pods labeled `=false` are never traced; their logs/pod-metrics dropped |
| `sensor.collection.nodeOptOutLabel` | `avuru.obs/collect` | Nodes labeled `=false` get no sensor pod at all |

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

Examples:

```bash
# Exempt a probe-sensitive workload from eBPF instrumentation + collection:
kubectl -n payments patch deploy checkout --type=merge \
  -p '{"spec":{"template":{"metadata":{"labels":{"avuru.obs/instrument":"false"}}}}}'

# Stop collecting an entire namespace:
helm upgrade avuruops ./avuruops --reuse-values \
  --set 'sensor.collection.excludeNamespaces={kube-system,kube-node-lease,kube-public,payments}'

# Pull the sensor off one node right now (no helm upgrade):
kubectl label node worker-3 avuru.obs/collect=false
```

If apps fail probes after installing the chart, start at
`docs/runbooks/app-probe-failures.md` — prefer these targeted opt-outs over
uninstalling.

## Downstream consumption

This chart is the canonical artifact. An enterprise overlay (separate repo)
layers Harbor image refs, Kustomize patches, and Keycloak/oauth2-proxy via
`helm template ./avuruops -f overlay-values.yaml | kustomize ...` — it never forks
the chart.

## Verification

`make e2e-helm` (from repo root) spins a kind cluster, installs the chart,
seeds deterministic OTLP, and asserts traces + correlated logs via the hub API.
