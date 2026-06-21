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

- Open-source governance layer: `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`,
  `MAINTAINERS.md`, and `.github/CODEOWNERS`.
- Release process: `RELEASING.md`, `RELEASE-CHECKLIST.md`, this changelog,
  `ROADMAP.md`, a root `VERSION` file, and a `release.yml` workflow.
- Contributor onboarding: expanded `README.md`, per-component READMEs
  (`agent/`, `hub/`, `ui/`), Avuru Enhancement Proposal (AEP) process in
  `design/`, issue templates, and `COMMIT-SIGNING-SETUP.md`.

### Changed

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
