# Changelog

All notable changes to avuru-obs are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) (`vX.Y.Z`). See
[RELEASING.md](RELEASING.md) for how versions are cut.

While the in-development version carries a `-SNAPSHOT` suffix (see
[`VERSION`](VERSION)), unreleased changes are collected under **Unreleased**.
When a release is cut, that block is renamed to the version with its date.

## [Unreleased]

## [0.1.0] — 2026-07-03

The first tagged release: **the wedge**. A fresh Kubernetes cluster reaches a
live service map in under five minutes with zero app changes — and that
promise is enforced as a CI gate. All four v0.1 signal tiers ship: traces
(Full), logs (Basic), continuous profiling (Lite) and infra metrics
(Supporting), plus the OTLP drop-in migration path.

### Added

- **Sensor DaemonSet** (`sensor.enabled=true`): per-node zero-code collection —
  OBI (`otel/ebpf-instrument`, eBPF traces + RED for every HTTP/gRPC service),
  a node collector (zero-config stdout/stderr logs with workload-derived
  service names; kubeletstats node/pod metrics), and an opt-in OTel eBPF
  profiler container (continuous CPU profiles at ~20 Hz). Kernel preflight
  (≥5.8 + BTF) warns
  loudly but never blocks; every container has its own switch.
- **Trace explorer**: search with tag/order/duration/status filters, latency ×
  time heatmap, per-operation RED overview, split workspace, span panel, six
  trace views (timeline, spans, flamegraph, statistics, graph, JSON) and
  structural **trace diff**; service map with call edges derived from spans.
- **Services inventory**: sortable RED table with drill-down to traces.
- **RED metrics dashboard**: bucketed rate/errors/latency charts per service
  (`GET /api/v1/metrics/red`).
- **Node & pod health**: latest utilization + trend sparklines and busiest
  pods (`GET /api/v1/infra/nodes`, `GET /api/v1/infra/pods`), backed by the
  five frozen `otel_metrics_*` ClickHouse tables (migration `0003`).
- **Continuous profiling** (experimental, opt-in via
  `sensor.profiler.enabled=true` — the upstream alpha loader hard-fails on
  some kernels): Coroot-style stack-dedup schema (migration `0004`), OTLP
  profiles ingest at `POST /v1development/profiles` isolated behind
  `hub/internal/storage/profilesadapter` (the alpha wire format never leaks
  past it), flame-graph API (`GET /api/v1/profiles/*`) and a click-to-zoom
  icicle UI.
- **Logs explorer**: full-text search, severity/service filters, `trace_id`
  correlation.
- **System Status**: component health, per-signal storage/retention/freshness
  (now including metrics and profiles), disk usage.
- **Gateway distro**: minimal OTel Collector built with OCB from
  `gateway/ocb-manifest.yaml` (published as `avuru-obs-gateway`); the stock
  contrib image remains a drop-in override.
- **The wedge gate**: `make e2e-helm` runs kind + Helm + a deliberately
  uninstrumented demo app and asserts the zero-code service map (edges
  included) within 300 seconds, plus infra metrics on the same clock — wired
  into CI.
- Per-signal retention knobs applied as ClickHouse TTLs by `hub migrate`:
  `retention.{traces,logs,metrics,profiles}`.
- Open-source governance layer: `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`,
  `MAINTAINERS.md`, and `.github/CODEOWNERS`.
- Release process: `RELEASING.md`, `RELEASE-CHECKLIST.md`, this changelog,
  `ROADMAP.md`, a root `VERSION` file, and a `release.yml` workflow.
- Contributor onboarding: expanded `README.md`, per-component READMEs
  (`agent/`, `hub/`, `ui/`), Avuru Enhancement Proposal (AEP) process in
  `design/`, issue templates, and `COMMIT-SIGNING-SETUP.md`.

### Changed

- The Helm chart deploys the full stack: hub (API) + UI (nginx) deployables,
  gateway, ClickHouse (or BYO), the migrate hook — and now the sensor
  DaemonSet.
- Adopted a Kiali-style trunk branch model: `main` is the single development
  trunk, with `vX.Y` release branches and `vX.Y.Z` tags (retired `develop`).
- Commit signing is now required (see `COMMIT-SIGNING-SETUP.md`).

### Deferred to v0.2

- The custom Rust eBPF L4 flow tracer and its flows schema (`0005`), the
  hub's OpAMP server + configuration UI, auth/OIDC (the enterprise seam —
  tenant column, provider interface, retention objects — ships in v0.1), and
  `proto/` buf codegen.

<!--
Release links:
[Unreleased]: https://github.com/avuruvision/avuru-obs/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/avuruvision/avuru-obs/releases/tag/v0.1.0
-->
