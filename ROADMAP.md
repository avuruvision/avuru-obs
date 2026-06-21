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

## Milestones toward v0.1

These milestone tags (`M1`–`M5`) are referenced throughout the codebase and
`agent_docs/`. Order is roughly sequential; details are directional.

| Milestone | Theme | Notable work |
|---|---|---|
| **M1** | Local stack & ingestion | `make dev` compose stack (ClickHouse + collector + demo app); OTLP ingest end-to-end; `proto/` codegen wired (buf); first e2e drop-in test |
| **M2** | Deployable OTLP backend | Helm install path; gateway → ClickHouse → Hub API working in-cluster |
| **M3** | Signal depth & correlation | Logs + trace correlation, infra metrics, query paths hardened _(directional)_ |
| **M4** | UI depth | Trace waterfall, flame-graph/chart library chosen, service-map polish |
| **M5** | Gateway build & TTV gate | OCB-built minimal collector distro; kind-based time-to-value gate (astronomy-shop profile) enforcing the <5-min wedge |

## Beyond v0.1 (directional)

- **v0.2 — auth:** OIDC behind the existing `hub/internal/auth.Provider`
  interface (v0.1 ships a local admin password). The
  [enterprise seam](agent_docs/architecture.md#enterprise-seam-do-not-bypass)
  — auth provider, `tenant` column, retention policy objects — is built in from
  v0.1 so this lands without a rewrite.
- **More clients:** the Hub API is the client-agnostic contract; the SPA is one
  thin client. A **Grafana** data source and a **CLI** are planned.
- **Storage re-evaluation:** ClickHouse stays behind `storage.Store`; GreptimeDB
  is slated for re-evaluation mid-2027 without changing Hub code.

## How this roadmap changes

Open an issue or a [discussion](CONTRIBUTING.md) to propose a change of
direction; open an [AEP](design/README.md) for anything that adds or alters a
[locked decision](agent_docs/architecture.md#locked-decisions-and-rationale).
Roadmap edits go through a normal PR.
