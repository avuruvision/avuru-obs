# Changelog

All notable changes to avuru-obs are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) (`vX.Y.Z`). See
[RELEASING.md](RELEASING.md) for how versions are cut.

While the in-development version carries a `-SNAPSHOT` suffix (see
[`VERSION`](VERSION)), unreleased changes are collected under **Unreleased**.
When a release is cut, that block is renamed to the version with its date.

## [Unreleased]

### Added

- **Per-project model (Coroot-style):** config-defined projects
  (`projects` chart value / `AVURUOPS_PROJECTS`) merged with tenants
  auto-discovered from data (`GET /api/v1/projects`); per-environment ingest
  tagging via `gateway.tenant` (stamps `avuru.tenant`, plus the profiler's
  ingest header); UI project switcher in the sidebar with shareable
  `?project=` links and project-scoped caches.
- **Collection controls:** deactivate collection per signal, per namespace
  (`sensor.collection.excludeNamespaces`), per pod (label
  `avuru.obs/instrument=false`), or per node (label
  `avuru.obs/collect=false`, instant — no upgrade). Full matrix in
  `deploy/helm/README.md`.
- **Agent inventory:** `GET /api/v1/agents` + Settings → Collection show
  per-node sensor freshness per signal ("N nodes reporting").
- **Sensor "do no harm" hardening:** CPU limits on all sensor containers,
  opt-in negative `PriorityClass` (on by default in the prod/staging
  overlays), and a diagnostics runbook + evidence script
  (`docs/runbooks/app-probe-failures.md`, `tools/diagnose/sensor-impact.sh`)
  for app pods failing probes after install.
- Settings screen restructured into General / Collection / Status tabs with
  shareable `?tab=` state.
- Chart render test suite (`make helm-check`) and an e2e-helm regression gate
  asserting pre-existing app pods stay healthy after the chart installs.

- Open-source governance layer: `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`,
  `MAINTAINERS.md`, and `.github/CODEOWNERS`.
- Release process: `RELEASING.md`, `RELEASE-CHECKLIST.md`, this changelog,
  `ROADMAP.md`, a root `VERSION` file, and a `release.yml` workflow.
- Contributor onboarding: expanded `README.md`, per-component READMEs
  (`agent/`, `hub/`, `ui/`), Avuru Enhancement Proposal (AEP) process in
  `design/`, issue templates, and `COMMIT-SIGNING-SETUP.md`.

### Changed

- **Default collection scope:** `kube-system`, `kube-node-lease`, and
  `kube-public` are no longer collected by default (traces, logs, pod
  metrics). Set `sensor.collection.excludeNamespaces: []` to restore the old
  behavior; node-level metrics are unaffected.
- Adopted a Kiali-style trunk branch model: `main` is the single development
  trunk, with `vX.Y` release branches and `vX.Y.Z` tags (retired `develop`).
- Commit signing is now required (see `COMMIT-SIGNING-SETUP.md`).

## [0.1.0] — planned

The first tagged release. Target scope is the **wedge**: a fresh Kubernetes
cluster reaches a live service map in under five minutes with zero app changes,
plus the v0.1 signal tiers (service map + RED, trace explorer, logs, CPU
profiling, infra metrics) and the OTLP drop-in migration promise. See
[ROADMAP.md](ROADMAP.md) for the milestone breakdown.

<!--
Release links — fill in once the repo's canonical remote is set, e.g.:
[Unreleased]: https://github.com/<org>/avuru-obs/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/<org>/avuru-obs/releases/tag/v0.1.0
-->
