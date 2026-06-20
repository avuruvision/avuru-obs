# Architecture

## North star

Fresh K8s cluster → live service map in **< 5 minutes**, zero app changes.
This is measured as a CI test (kind cluster + helm install + demo app + assert
via Hub API). See `agent_docs/testing.md`.

## System diagram

```
┌─ K8s cluster ──────────────────────────────────────────────────┐
│ DaemonSet "sensor" pod (multi-container):                      │
│   · avuru-agent (RUST, aya): L4 flow tracer → service map     │
│   · OBI container (Go, reused as-is): traces + RED metrics    │
│   · OTel Collector agent (reused): filelog tailer, kubeletstats│
│   · OTel eBPF profiler (reused as-is): CPU profiles           │
│ Cluster-agent (singleton): K8s objects/events                  │
│        │ OTLP                          ▲ OpAMP (config, keys)  │
│        ▼                               │                       │
│ Gateway: minimal OTel Collector distro─┘   ┌────────────────┐  │
│        │ batched inserts                   │ HUB (1 Go bin) │  │
│        ▼                                   │ API only       │  │
│ ClickHouse (traces·logs·metrics·profiles·flows) ◄─SQL─┘+OpAMP  │
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
4. **Service map path**: avuru-agent traces TCP `connect()`/`listen()` via
   eBPF → flow records → flows table. The map is **independent of traces** —
   it must light up even with zero instrumented apps and zero stitched traces.

## Locked decisions and rationale

| Decision | Choice | Why |
|---|---|---|
| Storage | ClickHouse, single store for all 4 signals + flows | Only engine with production-proven profiles storage (Coroot, qryn prior art); Apache 2.0; official beta OTel exporter; mature Go clients. Storage stays behind `storage.Store` — GreptimeDB re-evaluated mid-2027 |
| Zero-code traces/RED | OBI (OTel eBPF Instrumentation, ex-Beyla) reused as-is, sibling container | Apache 2.0/CNCF; HTTP/2, gRPC, SQL, Redis, Kafka coverage; OTLP-native; pre-1.0 so versions are pinned |
| Service map | Own Rust eBPF L4 flow tracer | Coroot's proven <5-min mechanic; de-risks the wedge from OBI's pre-1.0 trace-stitching limits |
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

## Enterprise seam (do not bypass)

- Auth: `hub/internal/auth.Provider` interface; v0.1 ships local admin
  password; OIDC lands v0.2 behind the same interface
- Tenancy: every ClickHouse table carries a `tenant` column (default `default`)
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
- Legacy Jaeger protocol (thrift/gRPC, non-OTLP): add the contrib
  `jaegerreceiver` to the gateway OCB manifest as a transition aid if needed.
- An e2e test (M1+) sends OTLP from a stock OTel SDK app with ONLY the
  endpoint env var set and asserts traces+logs land — guarding this promise.

## Kernel/degradation constraints

eBPF requires Linux ≥5.8 with BTF (full OBI trace stitching: ≥5.17). The
install preflight detects capability and degrades gracefully: no eBPF → still
logs + infra metrics + OTLP ingest from instrumented apps. Never hard-fail the
install on kernel capability.
