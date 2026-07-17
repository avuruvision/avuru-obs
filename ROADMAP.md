# Roadmap

Where avuru-obs is headed. This is **directional, not a commitment** — scope and
order shift as we learn. The authoritative, always-current technical detail
lives in [`agent_docs/architecture.md`](agent_docs/architecture.md); this file
is the human-readable summary for contributors and users. Larger items graduate
into [Avuru Enhancement Proposals](design/README.md) before implementation.

## North star

> Fresh Kubernetes cluster → live service map in **under 5 minutes**, zero app
> changes.

This is the wedge, and it is enforced as a CI gate (kind cluster + Helm install
+ demo app + assert via the Hub API). Every milestone below is judged against
it. See [AGENTS.md](AGENTS.md) for why "the wedge is law."

## v0.1 — the wedge (first tagged release)

The signal tiers we ship for 0.1 (from
[architecture.md](agent_docs/architecture.md#signal-depth-tiers-v01)):

| Tier | Signal |
|---|---|
| **Full** | Service map + RED metrics; trace explorer (waterfall, search) |
| **Basic** | Logs (stdout/stderr collection, full-text search, `trace_id` correlation) |
| **Lite** | Continuous profiling (per-service CPU flame graphs) |
| **Supporting** | Infra metrics (node/pod CPU, memory, network) |

Plus the hard product promise: **OTLP drop-in replacement** for Jaeger/OTLP
backends — already-instrumented apps migrate by changing only the exporter
endpoint, no SDK or code changes.

## Milestones toward v0.1 — ALL SHIPPED (v0.1.0)

These milestone tags (`M1`–`M5`) are referenced throughout the codebase and
`agent_docs/`. All five shipped in v0.1.0:

| Milestone | Theme | Shipped |
|---|---|---|
| **M1** | Local stack & ingestion | `make dev` compose stack; OTLP ingest end-to-end; first e2e drop-in test |
| **M2** | Deployable OTLP backend | Helm install path; gateway → ClickHouse → Hub API in-cluster; sensor DaemonSet (OBI zero-code traces + zero-config logs); services inventory UI |
| **M3** | Signal depth & correlation | Logs + trace correlation; kubeletstats infra metrics (schema → hub API → Nodes UI); RED dashboard |
| **M4** | UI depth | Trace waterfall/flamegraph/diff, split workspace; continuous profiling (ingest seam → flame-graph API → icicle UI) |
| **M5** | Gateway build & TTV gate | OCB-built minimal collector distro; kind-based time-to-value gate (uninstrumented wedge demo, <300 s service-map assertion) in CI |

## v0.2 (directional)

- **Auth:** OIDC behind the existing `hub/internal/auth.Provider`
  interface. The
  [enterprise seam](agent_docs/architecture.md#enterprise-seam-do-not-bypass)
  — auth provider, `tenant` column, retention policy objects — is built in from
  v0.1 so this lands without a rewrite.
- **v0.2 — runtime collection control plane:** v0.1 controls collection through
  Helm values (per-signal, per-namespace, per-pod label, per-node label — see
  `deploy/helm/README.md`) with a read-only agent inventory in Settings →
  Collection. v0.2 makes the same knobs switchable from the UI: the hub
  persists a bounded, schema-validated collection overlay and patches the
  sensor ConfigMaps (rollout via the existing config checksum), gated by a
  default-off flag and a namespace-scoped Role on the named resources only.
  OpAMP remains the destination — status reporting first, remote-config once
  OBI grows a client (AEP when it lands). Query-time filtering was rejected:
  it saves no collection or storage cost.
- **Projects (per environment):** v0.1 ships config-defined projects +
  auto-discovery from data (`projects`/`gateway.tenant` chart values, sidebar
  switcher, `?project=` links). Later: per-project **API keys** at ingest
  (needs v0.2 auth — replaces topology-based trust of `avuru.tenant` and the
  tenancy header), project CRUD (config-defined entries stay read-only),
  per-project retention (per-tenant TTL is its own design), per-project
  system status, and chart component toggles so secondary clusters install
  gateway(+sensor)-only against a shared ClickHouse.
- **Modules — pick your signals:** one switch per signal family
  (`modules.<name>.enabled`) gates its schema, API, pipeline, collection and UI
  together, so a traces-only install carries no log/profile weight. Everything
  is on by default; `core` (service map + traces + RED) always is. Shipped as
  the seam that new signals plug into — see the
  [AEP](design/2026-07-15-module-framework.md).
- **Error tracking (new module):** exceptions already reach us as span events
  and ERROR logs, but only as counts. This groups them into deduplicated
  issues with a stack trace, an occurrence timeline, links to the originating
  trace, and a triage lifecycle (resolved/ignored, plus regression detection
  when a resolved issue recurs). Two ways in, no code change either way:
  derived in-database from the OTLP you already send, and a Sentry-protocol
  ingest endpoint so existing SDKs — browser JS especially, the one signal
  eBPF cannot reach — point at avuru-obs by changing a DSN. Later: alerting,
  release tracking, source maps.
- **Wider ingest compatibility:** Jaeger, Zipkin, Prometheus and Loki push
  receivers alongside OTLP, plus forwarding exporters (OTLP/Kafka) so
  avuru-obs can dual-write during a migration. Extends the drop-in promise
  beyond OTLP and keeps the door open in both directions.
- **Network health on the service map:** per-edge RTT, retransmissions and
  resets delivered via OBI's built-in `network` feature (upstream OpenTelemetry
  eBPF Instrumentation) — connection-level health without traces, SDKs, or app
  changes.
- **Richer auto-tagging:** map Kubernetes labels/annotations to business tags
  and filter by them across every signal.
- **Deeper profiling:** off-CPU and memory profiles as the upstream OTel eBPF
  profiler grows them.
- **More clients:** the Hub API is the client-agnostic contract; the SPA is one
  thin client. A **Grafana** data source and a **CLI** are planned.
- **Storage re-evaluation:** ClickHouse stays behind `storage.Store`; GreptimeDB
  is slated for re-evaluation mid-2027 without changing Hub code.

## How this roadmap changes

Open an issue or a [discussion](CONTRIBUTING.md) to propose a change of
direction; open an [AEP](design/README.md) for anything that adds or alters a
[locked decision](agent_docs/architecture.md#locked-decisions-and-rationale).
Roadmap edits go through a normal PR.
