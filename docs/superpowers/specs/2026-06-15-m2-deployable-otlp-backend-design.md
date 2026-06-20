# M2 — Deployable OTLP Backend (Jaeger Replacement) + Correlated Logs

**Status:** approved design, ready for implementation plan
**Date:** 2026-06-15
**Branch:** `feature/m2-deployable-otlp-backend`
**Supersedes roadmap:** the original M2 (eBPF agent + service map) moves to M3.

## Goal

Package and harden the M1 engine (OTLP → ClickHouse → trace explorer) into a
**vendor-neutral Helm chart** that a user can `helm install` to **replace
Jaeger** for OTLP-exporting services (first cohort: the avuru-starters Spring
Boot services), **plus** a trace-correlated logs view. Migration stays a
one-line change: repoint `OTEL_EXPORTER_OTLP_*_ENDPOINT` at the chart's
gateway Service — the drop-in promise already proven by the M1 `make e2e`.

## Roadmap reorder (why this milestone exists)

The differentiating eBPF service-map wedge is higher-risk and not what the
user needs first. The immediate value is a **deployable Jaeger replacement**
for an ecosystem that already emits OTLP. So:

| | Original | Now |
|---|---|---|
| **M2** | eBPF agent + service map | **Deployable OTLP backend (Jaeger replacement) + correlated logs** |
| **M3** | logs + infra metrics | eBPF agent + service map (the wedge) |

The north-star TTV gate (kind + `helm install` + assert < 5 min) stays M5; M2
delivers a lighter `make e2e-helm` that proves the chart installs and ingests.

## Scope

**In:** Helm chart (gateway + single-node ClickHouse + hub + migration hook),
ClickHouse persistence + external toggle, hub-owned schema migrations, a
correlated logs API + `/logs` screen, per-signal retention, a no-op auth seam,
a kind-based install smoke test + CI job.

**Out (YAGNI — explicit):** eBPF agent / service map (M3), profiling, infra
metrics screen, OIDC/login UI, custom OCB collector distro, multi-node /
sharded ClickHouse, ClickHouse operator, Zookeeper/Keeper, OpAMP config path,
RED metrics from OBI. The chart is generic — Harbor image refs, Kustomize
overlays, and Keycloak wiring live in the user's **separate downstream GitLab
project**, not here.

## Locked decisions

| Decision | Choice | Rationale |
|---|---|---|
| Packaging | One generic Helm chart `deploy/helm/avuruops` as the product artifact | `architecture.md` locks Helm as day-0 bootstrap; downstream project consumes it |
| ClickHouse | Hand-rolled **single-node StatefulSet** + PVC + ConfigMap, no operator, no coordination layer | DeepFlow precedent; operator (SigNoz/Coroot) adds always-on Zookeeper/Keeper + CRD churn for zero single-node benefit |
| External CH | `clickhouse.external.enabled` toggle (in-chart CH not rendered when on) | Universal pattern — SigNoz/Coroot/DeepFlow all expose it; the "scale" path |
| Migrations | **Hub-owned**: versioned `.sql` `go:embed`'d in the hub binary, `schema_migrations` ledger, `hub migrate` subcommand | Raw `.sql` via a generic Job is the brittle option (no "already applied" — SigNoz's old pain); none of the 3 CH projects do it |
| Migration trigger | Helm **`pre-install,pre-upgrade` hook** Job (schema ready before the app rolls on both first install and upgrades) + same `hub migrate` path in compose/dev | Single mechanism both environments; SigNoz validates the hook primitive |
| Gateway | Pinned upstream `otel-collector-contrib` 0.154.0 image + our ConfigMap (no custom OCB distro yet) | Custom distro is an M-later optimization; reuse what compose already runs |
| Functional scope | Traces (hardened) **+ correlated logs** | Logs already ingest into CH (M1 seed proves it); correlation beats Jaeger cheaply |
| Auth | `auth.Provider` **no-op seam**, UI open; OIDC = v0.2 | User's Keycloak/oauth2-proxy covers auth in the downstream overlay |
| Retention | ClickHouse table TTL per signal via Helm values | `architecture.md`: TTL policy objects, not hardcoded |

## Chart topology

```
helm install avuruops →
  Deployment gateway   (otel-collector-contrib 0.154.0 + ConfigMap)
      → Service  OTLP 4317/gRPC + 4318/HTTP   ◄── instrumented apps
            │ batched ClickHouse exporter inserts
            ▼
  StatefulSet clickhouse  (single-node + PVC)   [skipped if external.enabled]
            ▲ SQL
  Deployment hub  (REST API + embedded SPA)
      → Service + Ingress                        ◄── browser
  Job hub-migrate  (Helm pre-upgrade hook, `hub migrate`)
```

Four workloads. No operator, no Zookeeper/Keeper.

## ClickHouse packaging

- **StatefulSet, `replicas: 1`**, `volumeClaimTemplates` → one stable PVC
  (`data-avuruops-clickhouse-0`) that the pod re-binds across restart/reschedule.
  This (not a Deployment) is what makes data survive pod churn.
- `persistence.storageClassName` (default `""` = cluster default SC; pin e.g.
  `ceph-rbd`/`longhorn` in the downstream overlay), `persistence.size`
  (default **50Gi**), `accessModes: [ReadWriteOnce]`.
- `resources`: requests `{cpu: 500m, memory: 2Gi}`, limits `{memory: 4Gi}` —
  the 2 GB-tuned config already proven in compose, shipped as a ConfigMap.
- PVCs intentionally **survive `helm uninstall`** (data safety); PV reclaim
  policy governs data on manual PVC deletion. Volume expansion requires the SC
  to allow it.
- `clickhouse.external.enabled: true` → StatefulSet + PVC not rendered; gateway
  and hub point at `clickhouse.external.address`, auth via `existingSecret`.

## Migrations (hub-owned)

- The `.sql` files relocate from `gateway/schemas/` to
  `hub/internal/storage/migrations/` and are `go:embed`'d into the hub binary.
- A `schema_migrations` ledger table tracks applied versions; `hub migrate` is
  idempotent (safe to run repeatedly; applies only unapplied versions).
- k8s: a Helm hook Job (`pre-install,pre-upgrade`) runs `hub migrate` against
  the in-chart or external ClickHouse before the hub/gateway roll — covering
  both first install and upgrades.
- compose/dev: the same `hub migrate` replaces the current init mechanism — one
  migration path everywhere, also reused by `make e2e`.

## Gateway & hub packaging

- **Hub:** Deployment + Service + Ingress (`ingress.enabled/host/className`),
  reusing the M1 distroless image with embedded SPA. New `migrate` subcommand.
- **Gateway:** Deployment + Service (4317/4318) + ConfigMap holding the
  collector pipeline (the M1 compose config, parameterized).
- Everything image-parameterized (`image.registry/repository/tag`) so the
  downstream overlay repoints to Harbor without forking templates.

## Correlated logs

Logs already land in ClickHouse (`otel_logs`); M2 surfaces them.

- **Hub API:** `GET /api/v1/logs` (full-text `q`, `service`, `severity`, time
  range, keyset cursor) and `GET /api/v1/traces/{id}/logs` (logs for a trace).
- **`/logs` screen:** dense table (time · severity badge · service · body ·
  `traceId` link), reusing M1 table/heatmap/keyset patterns and the Avuru Gold
  theme.
- **Correlation — in scope:** a log row's `traceId` opens its trace.
- **Correlation — stretch (only if M2 time allows):** a "logs" affordance on a
  waterfall span showing that span's logs (`span_id` filter). **Locked as
  stretch, not a commitment** — revisit at implementation.

## Retention / TTL

Per-signal ClickHouse table TTL, set from Helm values, applied by the
migration step. **Locked defaults: `retention.traces=7d`, `retention.logs=3d`**
(eval-friendly for single-node; overridable downstream).

## Auth

`auth.Provider` interface shipped as a **no-op provider** (UI open, nothing to
configure). `auth.enabled` defaults false. OIDC implementation = v0.2 behind
the same interface; the downstream Keycloak/oauth2-proxy overlay enforces auth
at the ingress for now.

## values.yaml surface (downstream consumption contract)

```yaml
image: { registry: "", repository: avuruops/hub, tag: "" }   # repoint to Harbor
gateway: { image: {...}, replicas: 1, resources: {...} }
ingress: { enabled: true, host: avuru.example.com, className: "" }
clickhouse:
  external: { enabled: false, address: "", database: otel, existingSecret: "" }
  persistence: { enabled: true, storageClassName: "", size: 50Gi }
  resources: { requests: {cpu: 500m, memory: 2Gi}, limits: {memory: 4Gi} }
retention: { traces: 7d, logs: 3d }
auth: { enabled: false }
```

## Verification

- **`make e2e-helm`:** create a kind cluster on the Colima VM → `helm install
  avuru` → drive/seed traffic → assert traces **and** correlated logs via the
  hub API + the UI responds. The M2 analogue of M1's `make e2e`; lighter than
  the M5 TTV gate but proves the chart installs and ingests for real.
- **CI:** path-filtered job on `deploy/helm/**` (and a `helm lint` /
  `helm template` schema check that needs no cluster).

## Deliverables

- `deploy/helm/avuruops/` — `Chart.yaml`, `values.yaml`,
  `templates/{gateway-deploy,gateway-svc,gateway-config,clickhouse-statefulset,clickhouse-config,clickhouse-svc,hub-deploy,hub-svc,ingress,migrate-job}.yaml`,
  `templates/_helpers.tpl`, `values.schema.json`.
- `hub`: `migrate` subcommand, `hub/internal/storage/migrations/` (embedded
  `.sql` + ledger), logs query methods on `storage.Store` + fake, logs API
  handlers, `auth.Provider` no-op seam.
- `ui`: `/logs` screen + logs data hooks + log→trace correlation.
- `make e2e-helm` + CI job.

## Open questions / risks

- **kind-on-Colima resource ceiling:** the single-node CH (2–4 GB) + gateway +
  hub on a kind node inside the Colima VM may be tight; `make e2e-helm` may
  need a reduced CH memory profile for CI. Validate early.
- **Span→logs stretch** may slip to M3 — acceptable; the log→trace direction is
  the committed correlation.
- **Gateway as upstream image vs OCB distro:** fine for M2; revisit if we need
  receivers/processors not in contrib.
