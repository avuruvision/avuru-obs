# Avuru Obs

**All-in-one observability — traces, metrics, logs, continuous profiling.
Live in 5 minutes, zero code changes.**

[![CI](https://github.com/avuruvision/avuru-obs/actions/workflows/ci.yml/badge.svg)](https://github.com/avuruvision/avuru-obs/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Roadmap](https://img.shields.io/badge/status-pre--v0.1-orange.svg)](ROADMAP.md)

Avuru Obs replaces the Grafana LGTM stack with one storage engine
(ClickHouse), one binary control plane, and one UI. eBPF auto-discovers your
services: install the Helm chart and watch the service map light up — no SDK,
no sidecars, no YAML archaeology.

> **Status: pre-v0.1, under active development.** See [ROADMAP.md](ROADMAP.md)
> for where it's headed and [`agent_docs/architecture.md`](agent_docs/architecture.md)
> for the living architecture.

## How it works

```
sensor DaemonSet (eBPF: flows · traces · RED · logs · profiles)
        │ OTLP
        ▼
gateway (minimal OTel Collector) ──► ClickHouse (all signals)
                                          ▲ SQL
hub (Go binary: API + OpAMP config plane)   ◄── UI (static SPA, own pod)
```

- **Zero-code**: OpenTelemetry eBPF Instrumentation (OBI) for traces + RED
  metrics; our Rust flow tracer for the live service map; OTLP ingest for
  apps you've already instrumented.
- **One store**: ClickHouse for traces, metrics, logs, profiles, and flows.
- **Drop-in**: already on OTLP/Jaeger? Point your exporter at the gateway —
  no SDK or code changes (a hard product requirement).

## Repository layout

| Path | What | Stack |
|---|---|---|
| [`agent/`](agent/README.md) | Node agent: eBPF L4 flow tracer feeding the service map | Rust (aya) |
| [`hub/`](hub/README.md) | Control plane: REST/WS API, OpAMP, alerting, storage interface | Go |
| [`ui/`](ui/README.md) | Static-export SPA (own nginx pod) | Next.js / TS |
| [`gateway/`](gateway/) | Minimal OTel Collector distro (OCB manifest) | OCB / YAML |
| [`proto/`](proto/README.md) | Shared contracts (codegen to Rust/Go/TS) | protobuf |
| [`sensor/`](sensor/README.md) | DaemonSet assembly (agent + OBI + collector + profiler) | YAML |
| [`deploy/`](deploy/helm/README.md) | Helm chart (flagship) + [docker-compose sandbox](deploy/compose/README.md) | Helm / compose |
| [`e2e/`](e2e/) | End-to-end tests (Go + Playwright) | Go / TS |
| [`tools/`](tools/) | Dev tooling (e.g. OTLP fixture seeder) | Go |
| [`agent_docs/`](agent_docs/README.md) | Topic docs for contributors (and AI agents) | — |
| [`design/`](design/README.md) | Avuru Enhancement Proposals (AEPs) | — |

## Getting started (contributors)

Read [AGENTS.md](AGENTS.md) first (yes, even humans — it's the canonical
developer guide) and [`agent_docs/development.md`](agent_docs/development.md).

**Prerequisites:** Go 1.26, Rust (stable), Node ≥22, Docker (Colima on macOS),
Helm 3, and GNU make.

```bash
make ui hub   # build the UI static export and the hub binary
make check    # everything CI runs (build + test + lint across components)
```

Per-component build/test/run lives in each component's README
([agent](agent/README.md), [hub](hub/README.md), [ui](ui/README.md)) and in
[`agent_docs/`](agent_docs/README.md). The eval path (Helm install / compose
sandbox) lands with the [M1–M2 milestones](ROADMAP.md).

## Contributing & community

| Doc | Purpose |
|---|---|
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to propose and submit changes |
| [GOVERNANCE.md](GOVERNANCE.md) | How decisions are made; becoming a maintainer |
| [MAINTAINERS.md](MAINTAINERS.md) | Who maintains what |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Community standards |
| [SECURITY.md](SECURITY.md) | Reporting vulnerabilities (privately) |
| [AI_POLICY.md](AI_POLICY.md) | Using AI tools when contributing |
| [COMMIT-SIGNING-SETUP.md](COMMIT-SIGNING-SETUP.md) | Required signed-commit setup |
| [design/](design/README.md) | Enhancement-proposal (AEP) process |
| [RELEASING.md](RELEASING.md) · [ROADMAP.md](ROADMAP.md) · [CHANGELOG.md](CHANGELOG.md) | Release process, direction, history |

New here? Look for **good first issue** labels, and open an issue or
[discussion](CONTRIBUTING.md) before non-trivial work.

## License

[Apache 2.0](LICENSE)
