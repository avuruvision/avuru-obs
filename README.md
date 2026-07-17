# Avuru Obs

**All-in-one observability — traces, metrics, logs, continuous profiling.
Live in 5 minutes, zero code changes.**

[![CI](https://github.com/avuruvision/avuru-obs/actions/workflows/ci.yml/badge.svg)](https://github.com/avuruvision/avuru-obs/actions/workflows/ci.yml)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/avuruvision/avuru-obs?color=blue)](https://github.com/avuruvision/avuru-obs/releases)

Avuru Obs keeps every signal — traces, metrics, logs, profiles — in one storage
engine (ClickHouse), behind one binary control plane and one UI. eBPF
auto-discovers your services: install the Helm chart and watch the service map
light up — no SDK, no sidecars, no YAML archaeology.

> **Status: v0.1.0 released**; `main` is under active development toward v0.2.
> See [ROADMAP.md](ROADMAP.md) for where it's headed and
> [`agent_docs/architecture.md`](agent_docs/architecture.md) for the living
> architecture.

## How it works

```
sensor DaemonSet (eBPF: traces · RED · logs · profiles)
        │ OTLP
        ▼
gateway (minimal OTel Collector) ──► ClickHouse (all signals)
                                          ▲ SQL
hub (Go binary: API + OpAMP config plane)   ◄── UI (static SPA, own pod)
```

- **Zero-code**: OpenTelemetry eBPF Instrumentation (OBI) for traces + RED
  metrics, with the live service map derived from those traces; OTLP ingest
  for apps you've already instrumented.
- **One store**: ClickHouse for traces, metrics, logs, profiles, and flows.
- **Drop-in**: already on OTLP/Jaeger? Point your exporter at the gateway —
  no SDK or code changes (a hard product requirement).

## Quickstart

**Kubernetes (the real install).** The chart is published to GHCR as an OCI
artifact — no repo to add:

```bash
helm install avuruops oci://ghcr.io/avuruvision/charts/avuruops \
  --version <X.Y.Z> -n avuruops --create-namespace
```

Or let the installer resolve the latest release and wait for rollout (it only
runs `helm`/`kubectl` against your current context — read it first if you like,
`--dry-run` prints the exact command):

```bash
curl -fsSL https://raw.githubusercontent.com/avuruvision/avuru-obs/main/deploy/install.sh | sh
```

The chart and every image we build are cosign-signed and multi-arch; see
[RELEASING.md](RELEASING.md#verifying-a-release) to verify a release.

**Laptop (no cluster, no checkout).** One compose file pulls the released
images and a demo app so the service map lights up immediately:

```bash
curl -fsSLO https://raw.githubusercontent.com/avuruvision/avuru-obs/main/deploy/compose/docker-compose.release.yaml
docker compose -f docker-compose.release.yaml up --wait   # then open http://localhost:3001
```

Full setup, sizing, and configuration: [`deploy/helm/README.md`](deploy/helm/README.md).

## Repository layout

| Path | What | Stack |
|---|---|---|
| [`hub/`](hub/README.md) | Control plane: REST/WS API, OpAMP, alerting, storage interface | Go |
| [`ui/`](ui/README.md) | Static-export SPA (own nginx pod) | Next.js / TS |
| [`gateway/`](gateway/) | Minimal OTel Collector distro (OCB manifest) | OCB / YAML |
| [`sensor/`](sensor/README.md) | DaemonSet assembly (OBI + collector + profiler) | YAML |
| [`deploy/`](deploy/helm/README.md) | Helm chart (flagship) + [docker-compose sandbox](deploy/compose/README.md) | Helm / compose |
| [`e2e/`](e2e/) | End-to-end tests (Go + Playwright) | Go / TS |
| [`tools/`](tools/) | Dev tooling (e.g. OTLP fixture seeder) | Go |
| [`agent_docs/`](agent_docs/README.md) | Topic docs for contributors (and AI agents) | — |
| [`design/`](design/README.md) | Avuru Enhancement Proposals (AEPs) | — |

## Getting started (contributors)

Read [AGENTS.md](AGENTS.md) first (yes, even humans — it's the canonical
developer guide) and [`agent_docs/development.md`](agent_docs/development.md).

**Prerequisites:** Go 1.26, Node ≥22, Docker (Colima on macOS),
Helm 3, and GNU make.

```bash
make ui hub   # build the UI static export and the hub binary
make check    # everything CI runs (build + test + lint across components)
```

Per-component build/test/run lives in each component's README
([hub](hub/README.md), [ui](ui/README.md)) and in
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

[AGPL-3.0](LICENSE)
