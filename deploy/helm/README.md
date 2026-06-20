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

No operator, no Zookeeper/Keeper — see the M2 design spec for the rationale
(validated against Coroot/SigNoz/DeepFlow/SkyWalking).

## Key values

| Value | Default | Purpose |
|---|---|---|
| `image.registry` | `""` | Prefix every image (e.g. `harbor.devops.lab`) for a private registry |
| `clickhouse.external.enabled` | `false` | BYO ClickHouse — set `.address` + `.existingSecret` |
| `clickhouse.persistence.storageClassName` | `""` | `""` = cluster default StorageClass |
| `clickhouse.persistence.size` | `50Gi` | PVC size |
| `retention.traces` / `retention.logs` | `7` / `3` | Per-signal TTL in days |
| `ingress.enabled` / `ingress.host` | `false` / `avuruops.local` | Expose the hub UI |
| `auth.enabled` | `false` | Forward placeholder — enforce auth at your ingress (OIDC is v0.2) |

## Downstream consumption

This chart is the canonical artifact. An enterprise overlay (separate repo)
layers Harbor image refs, Kustomize patches, and Keycloak/oauth2-proxy via
`helm template ./avuruops -f overlay-values.yaml | kustomize ...` — it never forks
the chart.

## Verification

`make e2e-helm` (from repo root) spins a kind cluster, installs the chart,
seeds deterministic OTLP, and asserts traces + correlated logs via the hub API.
