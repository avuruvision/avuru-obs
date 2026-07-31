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

## v0.2 — depth and control — ALL SHIPPED (v0.2.0)

Everything below shipped in v0.2.0; the full detail lives in
[CHANGELOG.md](CHANGELOG.md) and the linked AEPs.

| Theme | Shipped |
|---|---|
| **Auth & access control** | Secure-by-default login: local users, Admin/Editor/Viewer roles granted per project, server-side enforcement, anonymous access opt-in; **OIDC SSO** (any IdP — PKCE flow in the hub, group→role mapping, `forceSSO`) — [AEP](design/2026-07-21-auth-oidc-rbac.md) |
| **Module framework** | One switch per signal family (`modules.<name>.enabled`) gates schema, API, pipeline, collection and UI together; capabilities endpoint drives the sidebar — [AEP](design/2026-07-15-module-framework.md) |
| **Error tracking** | Deduplicated, triageable issues derived in-database from spans and logs, plus an opt-in Sentry-protocol ingest endpoint (browser SDKs report by changing a DSN) — [AEP](design/2026-07-16-error-tracking.md) |
| **Service health groups** | Group health with criticality tiers (T0/T1/T2), critical-dependency propagation, hot-reloadable config, `/health` tier-lane board — [AEP](design/2026-07-18-service-health-groups.md) |
| **Alerting** | Webhook notifications on service-health transitions: declarative rules, firing/resolved lifecycle, SSRF-guarded outbound, `/alerts` history — [AEP](design/2026-07-19-alerting.md) |
| **Network health** | Per-edge RTT + failed/reset connections from OBI TCP stats on the service-map edges (exact OBI stats key still to be confirmed in a real eBPF environment) — [AEP](design/2026-07-19-network-health.md) |
| **Green — energy & carbon** | Per-service Wh/gCO2e from Kepler (RAPL), carbon budgets, CSRD-ready export; off by default, honest no-RAPL reporting (real-RAPL validation still pending) — [AEP](design/2026-07-22-green-carbon.md) |
| **Sensor safe by default** | CI-enforced do-no-harm soak (probe-sensitive canary), `optIn` discovery mode, staged-rollout runbook — [AEP](design/2026-07-17-sensor-safe-by-default.md) |
| **Topology from OBI flows** | Service-map edges from OBI network-flow data; the cancelled Rust L4 tracer removed |
| **License** | Relicensed Apache-2.0 → **AGPL-3.0** |

## v0.3 — tenancy you can trust — ALL SHIPPED (v0.3.0)

Everything below shipped in v0.3.0; the full detail lives in
[CHANGELOG.md](CHANGELOG.md) and the linked AEPs.

| Theme | Shipped |
|---|---|
| **Projects — CRUD & demo (Phase 1)** | Admins create/rename/delete projects from the UI; built-in and config-defined entries stay read-only; a **one-click read-only demo** signs a visitor in as a scoped viewer with the shared password never leaving the server — [AEP](design/2026-07-27-projects-completion.md) |
| **Ingest API keys (Phase 2)** | Per-project keys validated in the gateway by an in-repo collector auth extension (the hub never enters the telemetry byte-path); in `enforce` the key's project is the authoritative tenant, replacing topology-based trust of `avuru.tenant`; default `log` mode keeps the drop-in promise intact — [AEP](design/2026-07-27-projects-completion.md) |
| **Green TDP estimation** | Power model for the RAPL-less nodes most fleets run on; every number labeled *estimated* end to end and never blended with measured joules; `/green` coverage panel — [AEP](design/2026-07-28-green-tdp-estimation.md) |
| **Runtime collection control — groundwork** | Overlay storage, closed-schema validation, `GET/PUT/DELETE /api/v1/collection/overlay`, default-off flag with a least-privilege namespaced Role. The applier is still a logging no-op and the UI stays read-only — [AEP](design/2026-07-27-collection-control-plane.md) |
| **Licensing & CLA** | [LICENSING.md](LICENSING.md) states the model in full (AGPL-3.0 community edition forever, per CLA §2.2); CLA bot live; `make notices` generates [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) |
| **Rename** | `avuruops` → `avuruobs` across the chart, `AVURUOBS_*` env prefix, mount paths, resource names and the green-quality attribute — **breaking**, see the upgrade note in [CHANGELOG.md](CHANGELOG.md) |

## v0.4 (directional)

Each of the larger items already has a draft AEP — design work done, awaiting
implementation.

- **Projects completion (Phase 3):** **member projects** (multi-cluster
  aggregation), per-project retention, per-project system status, and chart
  component toggles so secondary clusters install gateway(+sensor)-only
  against a shared ClickHouse. Phases 1 (CRUD + demo) and 2 (ingest keys)
  shipped in v0.3.0. See the
  [AEP](design/2026-07-27-projects-completion.md).
- **Runtime collection control plane — completion:** v0.3.0 shipped the
  overlay storage, schema validation, API and RBAC; what remains is the real
  applier (patch the sensor ConfigMaps, roll out via the existing config
  checksum) and the editable Settings → Collection UI, still behind the
  default-off flag. OpAMP remains the destination — status reporting first,
  remote-config once OBI grows a client. Query-time filtering was rejected: it
  saves no collection or storage cost. See the
  [AEP](design/2026-07-27-collection-control-plane.md).
- **Wider ingest compatibility:** Jaeger, Zipkin, Prometheus and Loki push
  receivers alongside OTLP, plus forwarding exporters (OTLP/Kafka) so
  avuru-obs can dual-write during a migration. Extends the drop-in promise
  beyond OTLP and keeps the door open in both directions. See the
  [AEP](design/2026-07-27-wider-ingest-compat.md).
- **Richer auto-tagging:** map Kubernetes labels/annotations to business tags
  and filter by them across every signal. See the
  [AEP](design/2026-07-27-auto-tagging.md).
- **More clients:** the Hub API is the client-agnostic contract; the SPA is one
  thin client. A **Grafana** data source and a **CLI** are planned. See the
  [AEP](design/2026-07-27-clients-grafana-cli.md).
- **Endpoint checks:** health when there is no traffic. See the
  [draft AEP](design/2026-07-20-endpoint-checks.md).
- **Deeper profiling:** off-CPU and memory profiles as the upstream OTel eBPF
  profiler grows them.
- **Storage re-evaluation:** ClickHouse stays behind `storage.Store`; GreptimeDB
  is slated for re-evaluation mid-2027 without changing Hub code.

## How this roadmap changes

Open an issue or a [discussion](CONTRIBUTING.md) to propose a change of
direction; open an [AEP](design/README.md) for anything that adds or alters a
[locked decision](agent_docs/architecture.md#locked-decisions-and-rationale).
Roadmap edits go through a normal PR.
