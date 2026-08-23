# Architecture

## North star

Fresh K8s cluster → live service map in **< 5 minutes**, zero app changes.
This is measured as a CI test (kind cluster + helm install + demo app + assert
via Hub API). See `agent_docs/testing.md`.

## System diagram

```
┌─ K8s cluster ──────────────────────────────────────────────────┐
│ DaemonSet "sensor" pod (multi-container):                      │
│   · OBI container (Go, reused as-is): traces + RED metrics    │
│   · OTel Collector agent (reused): filelog tailer, kubeletstats│
│   · OTel eBPF profiler (reused as-is): CPU profiles           │
│ Cluster-agent (singleton): K8s objects/events                  │
│        │ OTLP                          ▲ OpAMP (config, keys)  │
│        ▼                               │                       │
│ Gateway: minimal OTel Collector distro─┘   ┌────────────────┐  │
│        │ batched inserts                   │ HUB (1 Go bin) │  │
│        ▼                                   │ API only       │  │
│ ClickHouse (traces·logs·metrics·profiles) ◄─SQL─┘+OpAMP        │
│   single-node default (8 GB rec / 4 GB floor) | external = scale│
└────────────────────────────────────────────────────────────────┘
```

The **UI is a separate static SPA** (its own nginx pod), served single-origin
with the hub (`/`→UI, `/api`→hub). The hub API is the client-agnostic contract
— the SPA is one thin client; **Grafana and a CLI** are planned future clients.

## Data flow

1. **Telemetry path (bytes)**: sensor containers emit OTLP → gateway collector
   (enrich, batch, optional tail-sampling) → ClickHouse via the ClickHouse
   exporter. The Hub never touches this path.
2. **Query path**: UI (SPA) → Hub REST/WS → `storage.Store` interface →
   ClickHouse SQL. Purpose-built per-signal tables; no generic query engine.
3. **Config path**: agents/collectors register with ONE Hub URL → receive
   ingest endpoint, auth keys, and pipeline config over **OpAMP**
   remote-config. Day-0 bootstrap is Helm/operator; day-2 tuning is OpAMP.
4. **Service map path**: the service map is derived from OBI trace spans —
   cross-service Client/Server span pairs give the call edges. OBI's built-in
   `network` feature (metric `obi.network.flow.bytes`, with k8s src/dst
   identity) enriches it with edges for un-instrumented services, so the map
   still lights up where there are no stitched traces. Both sources see
   service-mesh proxies and ingress gateways as ordinary peers, so
   `internal/topology` classifies those workloads as **transport** and the map
   hides them by default — otherwise every `app → proxy → app` hop renders as
   two application dependencies (`AVURUOBS_TOPOLOGY_CONFIG`,
   design/2026-08-23-service-map-transport.md).

## Locked decisions and rationale

| Decision | Choice | Why |
|---|---|---|
| Storage | ClickHouse, single store for all 4 signals | Only engine with production-proven profiles storage (Coroot, qryn prior art); Apache 2.0; official beta OTel exporter; mature Go clients. Storage stays behind `storage.Store` — GreptimeDB re-evaluated mid-2027 |
| Zero-code traces/RED | OBI (OTel eBPF Instrumentation, ex-Beyla) reused as-is, sibling container | Apache 2.0/CNCF; HTTP/2, gRPC, SQL, Redis, Kafka coverage; OTLP-native; pre-1.0 so versions are pinned |
| Service map | OBI trace-derived edges + OBI `network` flow edges | Reuses upstream OBI (CNCF, Apache-2.0) for both span-derived and un-instrumented edges; no bespoke privileged agent to build or maintain |
| Profiling | OTel eBPF profiler as Collector receiver | OTLP Profiles signal is **alpha** → profile ingestion isolated behind an adapter so wire-format breaks don't ripple into storage |
| Hub | **API-only** single Go binary: API + OpAMP + alerting; SQLite app-state, Postgres for HA | SigNoz retreated from microservices to exactly this. The API is the **client-agnostic contract** for all clients (SPA, Grafana, CLI). |
| UI packaging | Next.js `output: 'export'` static SPA in its **own nginx pod** (separate deployable), single-origin with the hub (`/`→UI, `/api`→hub). UI is one thin client among several (Grafana/CLI later) | Decouples UI from backend so any client plugs into the same API; lets the UI scale/resource independently |
| Sampling | 100% ingestion default; tail sampling = first opt-in knob | Missing traces destroy first-touch trust; ClickHouse compression makes full fidelity cheap at eval scale |
| Pipeline | DaemonSet sensor + singleton cluster-agent + OCB gateway | Cluster-agent avoids N-nodes-duplicate cluster metrics; gateway owns batching/sampling |

## Signal depth tiers (v0.1)

- **Full**: service map + RED, trace explorer (waterfall, search)
- **Basic**: logs (stdout/stderr collect, full-text search, trace_id correlation)
- **Lite**: profiling (CPU flame graphs per service only)
- **Supporting**: infra metrics (kubeletstats: node/pod CPU/mem/network)

## Modules (which signal families an install runs)

A **module** is one signal family gated end to end by a single switch
(`modules.<name>.enabled` → `AVURUOBS_MODULES`): schema, Hub API routes,
gateway pipeline, sensor collection, and UI entry. Everything is enabled by
default, so this is subtraction only — never install friction. See
[the AEP](../design/2026-07-15-module-framework.md).

| Module | Owns | Notes |
|---|---|---|
| `core` | traces, service map, RED, projects, system status | **No switch** — this is the wedge. RED is trace-derived, so the Metrics view is core |
| `logs` | `otel_logs`, log search + trace correlation | |
| `infra-metrics` | the `otel_metrics_*` tables, `/infra/*`, sensor inventory | The inventory reads collector self-metrics from these tables |
| `profiling` | `profiling_*`, flame graphs, profiles ingest | |
| `green` | `/api/v1/green/*`, `/green` UI, carbon budgets, CSRD export; owns **no tables** (read-time Wh/gCO2e aggregation over `otel_metrics_*`) | **Born off** — the energy signal needs RAPL hardware, so a default-on module would silently flip on across a fleet on upgrade. Requires `infra-metrics` (the pod→workload join); collection is a second opt-in (`sensor.green.enabled`, the Kepler container). See `design/2026-07-22-green-carbon.md` |

Registry: `hub/internal/modules` (Go) mirrored in `ui/src/lib/api-types.ts`;
migrations are tagged per module in `migrations.ByModule`. Clients discover the
active set from **`GET /api/v1/capabilities`** — the client-agnostic contract
(the SPA hides inactive entries; a hub predating the endpoint means "all on").
Disabling a module never drops existing tables; it stops managing them.

## Enterprise seam (do not bypass)

- Auth: `hub/internal/auth` (sessions, local users, fixed roles × per-project
  grants, anonymous demo identity) — shipped; OIDC SSO shipped behind the same
  `auth.Service` seam (AEP 2026-07-21). The hub itself runs the
  authorization-code + PKCE flow (`GET /api/v1/auth/oidc/start` → IdP →
  `GET /api/v1/auth/oidc/callback`) and mints the same server-side session as
  a local login; IdP groups → grants mapping is applied at **read time** on
  every request (group moves re-scope access without re-login). Config is a
  mounted YAML file (`AVURUOBS_AUTH_OIDC_CONFIG`, hot-reloaded ~15s; client
  secret separately via `AVURUOBS_AUTH_OIDC_CLIENT_SECRET`); IdP discovery is
  fail-loud at startup, and `AVURUOBS_PUBLIC_URL` must be the install's
  external base URL — it builds the absolute redirect_uri IdPs require.
  `forceSSO` only hides the UI's password form; the local admin API login
  remains as break-glass
- Tenancy: every ClickHouse table carries a `tenant` column (default `default`)
- Ingest authentication: per-project keys (`auth_ingest_key`, SHA-256 only —
  the raw key is shown once at creation and never stored). Validation happens
  **in the gateway**, via the in-repo `avuruingestauth` collector extension
  calling the hub's `POST /internal/v1/ingest-keys/validate` (guarded by a
  chart-generated shared token). **The hub is never in the telemetry
  byte-path** — it answers a control-plane question and nothing more, so hub
  availability bounds key *changes*, not ingest. The extension caches positive
  AND negative verdicts (30 s) and serves stale ones through a hub outage
  (5 min grace), failing open in `log` and closed in `enforce`.
  `auth.ingest.mode` is the rollout dial: `off` | `log` (default) | `enforce`.
  Only `enforce` wires the `tenantfromauth` processor, which stamps
  `avuru.tenant` from the validated key's project — it must run **after**
  `resource/tenant`, whose upsert would otherwise win. `log` therefore leaves
  the pipeline byte-identical to a pre-ingest-keys install, which is what keeps
  the drop-in promise below intact across the upgrade
- Retention: per-signal TTL policy objects, not hardcoded TTLs

## Migration requirement: drop-in replacement for Jaeger/OTLP backends

Apps already instrumented with the OTel SDK (e.g. Spring Boot services
exporting OTLP to Jaeger) MUST migrate by changing **only the exporter
endpoint address** — the gateway exposes standard OTLP on 4317 (gRPC) and
4318 (HTTP) for traces, metrics, AND logs. This is a hard product requirement
(first migration cohort: the avuru-starters Java services):

- Never require SDK, dependency, or code changes for OTLP senders.
- Log correlation works two ways: zero-change (stdout collected by the
  sensor, `traceId`/`spanId` parsed from the log pattern) or structured
  (OTel logback appender → same OTLP endpoint).
- Non-OTLP senders (v0.6): the gateway binary carries the contrib
  `jaeger`, `zipkin`, `prometheusremotewrite` and `loki` receivers, each
  behind its own `gateway.receivers.<proto>.enabled` values flag (all default
  off, ports 14250/14268 · 9411 · 9291 · 3100). They join the SAME
  `transform/tenant → resource/tenant → tenantfromauth → batch` stage as
  OTLP, so ingest-key enforcement is protocol-agnostic — no protocol gets a
  side door. Jaeger UDP/thrift is deliberately NOT offered (no authenticator
  hook, and jaeger-agent is deprecated upstream); the pinned
  remote-write receiver is v2-only, so a v1 sender gets 415.
- Leaving is as supported as arriving: `gateway.forward.{otlp,kafka}` dual-write
  every enabled signal to a second backend, with a bounded sending queue so a
  dead legacy target can never backpressure the ClickHouse path.
- An e2e test (M1+) sends OTLP from a stock OTel SDK app with ONLY the
  endpoint env var set and asserts traces+logs land — guarding this promise.
  The wider claim is guarded the same way: `make e2e-compat` (compose,
  opt-in) and the kind `e2e-helm` leg each send one real fixture per
  protocol through the chart-rendered receivers and assert the rows in
  ClickHouse, plus the dual-write reaching a stand-in legacy backend.

## Kernel/degradation constraints

eBPF requires Linux ≥5.8 with BTF (full OBI trace stitching: ≥5.17). The
install preflight detects capability and degrades gracefully: no eBPF → still
logs + infra metrics + OTLP ingest from instrumented apps. Never hard-fail the
install on kernel capability.
