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

- **Authentication & per-project access control (secure by default).** The hub
  now requires login: local users with fixed roles — Admin, Editor, Viewer —
  granted per project (or `*` for all), enforced server-side on every API
  route. The `X-Avuru-Tenant` header is validated against the caller's grants,
  turning projects into a real security boundary: a user granted only
  `staging` gets 403 anywhere else and a switcher that lists only `staging`.
  Fresh installs bootstrap an `admin` user (password in the release Secret —
  see the install NOTES); `auth.enabled=false` restores the previous open
  behavior. Opt-in **anonymous access** grants visitors a role on an explicit
  project list only — a public demo can share one project while every other
  project stays invisible. Sessions are server-side (revocation is
  immediate); logins are rate-limited; state lives in ClickHouse — no new
  components. OIDC/SSO and per-project ingest keys land next on the same
  seam (AEP `design/2026-07-21-auth-oidc-rbac.md`).
- **Module framework — pick your signals.** One switch per signal family
  (`modules.<name>.enabled`) gates it end to end: its ClickHouse schema
  (`hub migrate` skips the DDL), its Hub API routes (404 when off), its gateway
  pipeline, its sensor collection, and its UI entry — so a traces-only install
  carries no log or profile weight. The service map + traces + RED `core` is
  always on and has no switch. Everything defaults on, so an existing install
  upgrades unchanged; turning a module on later is a values change plus
  `helm upgrade` (the migrator is idempotent and applies the newly-active DDL,
  and disabling never drops tables). An install advertises its active set at
  `GET /api/v1/capabilities`: the UI sidebar follows it, and a module-off page
  prints the exact `helm upgrade --set` hint for direct links and bookmarks.
  See [`design/2026-07-15-module-framework.md`](design/2026-07-15-module-framework.md).
- **Error tracking** — a new module (`modules.errorTracking.enabled`, default
  on). Exceptions already reaching avuru-obs as span events, error spans and
  ERROR/FATAL logs are grouped into deduplicated, triageable **issues**: a
  stack trace, an occurrence timeline and histogram, a link to the originating
  trace, and a triage lifecycle (resolved/ignored) that flags a regression when
  a resolved issue recurs. Derived in-database from the OTLP you already send,
  so it needs no code change and no extra collection. See
  [`design/2026-07-16-error-tracking.md`](design/2026-07-16-error-tracking.md).
- **Sentry-protocol ingest** — opt-in (`gateway.sentry.enabled`, off by
  default; it opens a network surface). A gateway receiver on `:4319` accepts
  existing Sentry SDKs — browser JavaScript especially, which eBPF cannot
  reach — so an app reports by changing its DSN, with no SDK swap. Requires the
  `error-tracking` and `logs` modules (events are stored as log records);
  accepted browser origins are configurable via `gateway.sentry.allowedOrigins`.
- **Service-map edges derived from OBI network flows.** The sensor now builds
  topology from OBI's network-flow data, widening the map beyond the protocols
  zero-code instrumentation parses.
- **Service health groups** — a new module (`modules.serviceHealth.enabled`,
  default on). Operator-declared service groups with criticality tiers
  (T0/T1/T2), a composite status per group derived from the RED data already
  collected, critical-dependency propagation, and a `/health` tier-lane board
  in the UI. Config is hot-reloadable (a ConfigMap edit re-tiers services with
  no restart); unmatched services auto-group by namespace so a zero-config
  install still gets a useful board. See
  [`design/2026-07-18-service-health-groups.md`](design/2026-07-18-service-health-groups.md).
- **Alerting** — a new module (`modules.alerting.enabled`, default on).
  Webhook notifications when a service or group crosses into a bad state,
  driven by the service-health status stream: declarative rules in values, an
  evaluator with firing/resolved transitions, alert history, and a read-only
  `/alerts` UI page. Outbound webhooks are SSRF-guarded
  (`alerting.webhookAllow`). See
  [`design/2026-07-19-alerting.md`](design/2026-07-19-alerting.md).
- **Network health on the service-map edges** — per-edge RTT and failed/reset
  connection counts from OBI's TCP-stats metrics
  (`sensor.obi.network.stats`, on with `sensor.obi.network.enabled`),
  surfaced as edge tooltips and health styling on the map. The exact OBI
  stats key still needs confirmation in a real eBPF environment before prod
  use. See
  [`design/2026-07-19-network-health.md`](design/2026-07-19-network-health.md).
- **Green energy & carbon** — a new module (`modules.green.enabled`, **off by
  default**: the signal depends on RAPL/powercap hardware). Per-service energy
  (Wh) and carbon (gCO2e) computed from the energy counters of CNCF Kepler —
  an opt-in fourth sensor container (`sensor.green.enabled`), pinned like every
  upstream we reuse — correlated with the pod→workload map the platform
  already collects: zero code changes, no data leaves the cluster, no external
  API. Ships monthly carbon budgets per service group (warn at 80%, exceeded
  at 100%, month-end projection) delivered through the existing alerting
  channels, per-request carbon intensity, a `/green` dashboard with a
  service-map energy overlay, and a CSRD-ready CSV/JSON export whose
  methodology block states the formula, factor provenance and measurement
  coverage — numbers an auditor can reproduce. Grid-intensity factors are
  bundled per-country annual averages with operator overrides (air-gap
  friendly); all math runs at query time over tables that already exist, so
  there is no migration. On nodes without RAPL the module reports honestly
  instead of estimating (coverage ratio + a teaching empty state), and the
  Kepler container carries no probes so it can never destabilize the sensor
  pod. Kepler's metric names, config keys and port are CI-validated against
  the pinned image but **must be confirmed on real RAPL hardware before
  production use**. See
  [`design/2026-07-22-green-carbon.md`](design/2026-07-22-green-carbon.md).
- **The sensor is now provably safe to leave on.** The e2e wedge gate keeps a
  probe-sensitive canary — tight CPU limit, aggressive liveness probe, real
  traffic — Ready with zero restarts through a soak with the sensor attached,
  so "installing avuru-obs does no harm" is CI-enforced where it actually
  bites. For cautious fleets, `sensor.obi.discovery.mode=optIn` attaches
  uprobes only to pods labeled `avuru.obs/instrument: "true"` (logs, infra
  metrics and the inventory keep flowing), and a staged-rollout runbook
  (`docs/runbooks/sensor-rollout.md`) covers canary node pools, soak, and the
  escape-hatch ladder. See
  [`design/2026-07-17-sensor-safe-by-default.md`](design/2026-07-17-sensor-safe-by-default.md).

### Changed

- **Relicensed from Apache-2.0 to AGPL-3.0.**

### Removed

- **The cancelled Rust eBPF L4 flow tracer** (`agent/`), together with the
  `proto/` cross-language contract that existed to carry its `flow.proto`.
  Service-map topology now derives from OBI network flows instead, so the
  custom tracer, its flows schema and its codegen are no longer planned.

### Security

- **UI image OS packages patched at build.** The nginx-alpine base lagged behind
  Alpine's security fixes (Harbor flagged OpenSSL/zlib/libexpat CVEs); the UI
  Dockerfile now runs `apk upgrade` so each build ships the patched packages. A
  new CI `image-scan` job builds every image and fails on fixable HIGH/CRITICAL
  CVEs (Trivy, `--ignore-unfixed`) to keep it from regressing.

## [0.1.0] — 2026-07-15

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
- **Deep trace inspect**: resizable/expandable span detail with
  copyable attributes, per-span tree view, derived span status and component
  detection, service perspective from inside a trace (focus dimming,
  participant-filtered drill-down), span-id lookup, service/operation filter
  autocomplete, and a trace list groupable by service.
- **Services inventory**: sortable RED table with drill-down to traces.
- **RED metrics dashboard**: bucketed rate/errors/latency charts per service
  (`GET /api/v1/metrics/red`).
- **Node & pod health**: latest utilization + trend sparklines and busiest
  pods (`GET /api/v1/infra/nodes`, `GET /api/v1/infra/pods`), backed by the
  five frozen `otel_metrics_*` ClickHouse tables (migration `0003`).
- **Continuous profiling** (experimental, opt-in via
  `sensor.profiler.enabled=true` — the upstream alpha loader hard-fails on
  some kernels): stack-deduplicated profile schema (migration `0004`), OTLP
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
- **Per-project model:** config-defined projects
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

- The Helm chart deploys the full stack: hub (API) + UI (nginx) deployables,
  gateway, ClickHouse (or BYO), the migrate hook — and now the sensor
  DaemonSet.
- **Default collection scope:** `kube-system`, `kube-node-lease`, and
  `kube-public` are no longer collected by default (traces, logs, pod
  metrics). Set `sensor.collection.excludeNamespaces: []` to restore the old
  behavior; node-level metrics are unaffected.
- Adopted a trunk-based branch model: `main` is the single development
  trunk, with `vX.Y` release branches and `vX.Y.Z` tags (retired `develop`).
- Commit signing is now required (see `COMMIT-SIGNING-SETUP.md`).

### Deferred to v0.2

- The hub's OpAMP server + configuration UI, and auth/OIDC (the enterprise
  seam — tenant column, provider interface, retention objects — ships in
  v0.1).

<!--
Release links:
[Unreleased]: https://github.com/avuruvision/avuru-obs/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/avuruvision/avuru-obs/releases/tag/v0.1.0
-->
