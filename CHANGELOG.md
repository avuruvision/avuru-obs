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

- **Full user management from the UI.** Settings → Users now edits a user's
  name and role grants, resets passwords (with every session of the affected
  user signed out), and **deletes** users — an explicit second step available
  only after disabling, amending the original disable-only decision
  (design/2026-08-06-users-crud-password.md). A new Settings → Account tab
  lets any signed-in local user change their own password (current password
  required; other sessions are evicted, the active one stays). Password
  operations are refused for SSO users — their credential lives at the
  identity provider.

### Security

- **An admin could mint a working local password for an SSO-only account.**
  `PUT /api/v1/users/{id}` accepted a `password` for any user regardless of
  origin, and neither the email lookup nor the password check filtered on it —
  so the new credential was a genuine, working login that bypassed the
  identity provider along with its MFA and conditional-access policy. Password
  edits are now allow-listed on `origin=local` (a future origin defaults to
  refused), on both the admin route and the new self-service one. Deleting an
  SSO user is also now spelled out in the UI as what it is: it removes only
  the local record, and because `disabled` is the flag the SSO callback
  checks, deleting a *disabled* SSO user **undoes** their lockout.

### Fixed

- **Logging in through a reverse proxy that rewrites `Host` no longer 403s.**
  The hub's CSRF check compared the browser's `Origin` against the `Host` it
  received, so any proxy handing the cluster its ingress address instead of the
  public domain turned every write — the login POST first of all — into
  `cross-origin request rejected`. Two new chart values fix it without touching
  the default, which stays strict: `auth.trustedOrigins` names the origins that
  are legitimate despite not matching `Host` (the check stays on for everything
  else), and `auth.originCheck` (`enforce` | `log` | `off`) lowers it when the
  origins can't be enumerated — `log` allows the write and records the
  `Origin`/`Host` pair, which is how you find out what a proxy actually sends.
  An install that sets neither renders the same manifest and behaves exactly as
  before. When `auth.oidc.publicUrl` is set it is trusted automatically.

- **A default `helm install` now pulls the images it is supposed to.** The
  chart's image defaults never matched what the release workflow publishes, in
  two independent ways: the repositories read `avuruobs/hub` (Docker Hub) while
  releases push `ghcr.io/avuruvision/avuru-obs-hub`, and the tag defaults to
  `.Chart.AppVersion` — bare SemVer, no leading `v` — while only `vX.Y.Z` and
  `vX.Y` were ever pushed. Both are fixed: the four first-party repositories
  now point at GHCR, and the release workflow additionally publishes the bare
  `X.Y.Z` tag. Installs that already pass `--set …repository`/`…tag` (CI e2e,
  private-registry overlays via `image.registry`) are unaffected.
- **Green TDP estimation had no image at all.** `sensor.green.estimation.image`
  shipped with an empty repository and no tag default, so enabling the feature
  rendered an unusable image reference. It now defaults to the published
  `avuru-obs-tdp-estimator` at the chart's app version, and `make version-set`
  stamps that image alongside the other three.

## [0.3.0] — 2026-07-31

**Tenancy you can trust.** v0.2 secured the read side — login, roles,
per-project grants, SSO. v0.3 closes the write side and makes the project
itself a thing you administer: create, rename and delete projects from the UI,
then mint **per-project ingest keys** so a sender no longer just *claims* a
tenant — in `enforce` mode the key decides where its telemetry lands,
overriding anything the payload says. Around that: a **one-click read-only
demo** anyone can click through, **green energy on RAPL-less cloud VMs** (the
majority of real fleets), the groundwork for runtime collection control, and
the deploy layer renamed to match the project — `avuruops` → **`avuruobs`**
(breaking; see Changed).

### Added

- **Per-project ingest API keys (Phase 2).** Telemetry can now be
  **authenticated at the write side**, replacing topology-based trust of a
  client-supplied `avuru.tenant`. Admins mint keys in Settings → General →
  Ingest API keys (or `POST /api/v1/projects/{project}/keys`); the raw secret is
  shown **exactly once** and only its SHA-256 is stored. The gateway validates
  keys through a new in-repo collector extension (`avuruingestauth`) against a
  hub control-plane endpoint — **the hub is never in the telemetry byte-path** —
  with a 30 s verdict cache and a 5 min stale grace so a hub blip cannot drop
  traffic.
  Rolled out through `auth.ingest.mode`:
  - `off` — no key checking.
  - `log` (**default**) — validate and count would-be denials, reject nothing.
    The pipeline is byte-identical to a pre-ingest-keys install, so **the
    drop-in OTLP promise survives the upgrade**: existing unkeyed senders keep
    landing unchanged.
  - `enforce` — unkeyed or invalid OTLP is rejected, and the key's project
    becomes the **authoritative tenant**, overriding anything the sender claims.
    A sender that lies about its tenant lands where its key says.

  The chart provisions and seeds the sensor's own key, so enabling `enforce`
  never silences avuru's own agent. The internal token and sensor key are
  generated once, reused across upgrades, and live only in a Secret — asserted
  at render time.
- **UI-managed projects (Phase 1).** Projects now have a persistent identity you
  control from the app. Admins **create, rename, and delete** projects in
  Settings → General; the switcher and General tab reflect them immediately,
  while the built-in `default` and deployment-config projects stay read-only
  (clearly labelled). A project's id is an immutable tenant slug; only its
  display name is editable, so no telemetry is ever rewritten or lost — delete
  removes the entry and its data ages out by retention. New admin endpoints:
  `POST/PUT/DELETE /api/v1/projects` (global-admin only); `GET /api/v1/projects`
  now returns each project's `label`, `source`, and `editable` flag. Groundwork
  for per-project ingest keys and multi-cluster aggregates (Phases 2–3).
- **One-click read-only demo.** A "Try the demo" button on the login page signs
  a visitor in as a scoped viewer (`viewer@demo`) — the shared password stays
  server-side (a rate-limited `/api/v1/auth/demo`), never in the browser.
  Opt-in via `auth.demo.enabled`; pair with the OpenTelemetry Astronomy Shop
  overlay ([deploy/demo/astronomy](deploy/demo/astronomy)) tagged
  `avuru.tenant=demo` for live data across every module.
- **Runtime collection control — control-plane groundwork.** The hub can now
  store and serve a bounded, schema-validated **collection overlay**
  (`GET/PUT/DELETE /api/v1/collection/overlay`): whole-signal on/off plus the
  shared namespace-exclusion list, as a closed schema — no free-form collector
  YAML is ever accepted from a client, so the API adds no injection surface.
  Gated by `collection.runtimeControl.enabled` (**default off**), which also
  provisions a dedicated ServiceAccount and a namespaced Role scoped to the
  four named sensor ConfigMaps and the one named sensor DaemonSet — nothing
  cluster-wide. This release ships the storage, validation, API and RBAC only:
  the applier is a logging no-op and Settings → Collection stays read-only, so
  an overlay is persisted but does **not** yet change what the sensor
  collects. Editing collection at runtime lands in a later release; keep using
  Helm values. See the
  [AEP](design/2026-07-27-collection-control-plane.md).

- **Licensing clarity.** [LICENSING.md](LICENSING.md) states the model in
  full: AGPL-3.0 community edition forever (backed by the CLA §2.2 pledge),
  the node agent as upstream Apache-2.0 OBI, a planned commercial enterprise
  edition that only ever adds, and dual licensing for embedders.
  `make notices` generates [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)
  (Apache §4 attribution for bundled dependencies) and is now a release
  checklist step. The UI package now declares `AGPL-3.0-only` explicitly.
- **Contributor License Agreement live.** Every first-time contributor signs
  the [Individual CLA](CLA.md) via a one-comment bot flow; §2.2 pledges all
  contributions remain available under AGPL-3.0 forever.
- **Green TDP estimation for RAPL-less nodes.** The green module now works on
  the infrastructure most teams actually run: on a node with no RAPL/powercap
  — the overwhelming majority of public-cloud VMs — a new opt-in estimator
  models CPU power from utilization instead of leaving `/green` empty. Every
  number it produces is stamped **estimated** end to end (SQL, API, UI, and
  the CSRD export's methodology block) and is never blended with real
  RAPL-measured numbers, so what you see is always honestly labeled — trend
  and regression grade (±30-50% typical error), never presented as
  audit-grade. `/green` gains a coverage panel (known/measured/estimated/
  absent nodes) that finally makes the RAPL-less share visible instead of
  silently invisible, and carbon budgets include estimated energy (so an
  all-VM fleet's budget can still trip) while flagging how much of a
  threshold breach is modeled versus measured. Opt in with
  `sensor.green.estimation.enabled` (requires `sensor.green.enabled`); the
  bundled CPU power-coefficient table is sourced and cited (Cloud Carbon
  Footprint, cross-checked against the original SPECpower-derived notebook).
  See the [AEP](design/2026-07-28-green-tdp-estimation.md).

### Changed

- **BREAKING — `avuruops` is now `avuruobs` everywhere.** The deploy layer and
  the env-var contract now match the project's actual name. Renamed: the Helm
  chart (`deploy/helm/avuruobs`, published at
  `oci://ghcr.io/<org>/charts/avuruobs`), the `AVURUOPS_*` environment-variable
  prefix (→ `AVURUOBS_*`), the config mount paths, the generated Kubernetes
  resource names, and the green-quality telemetry attribute
  `avuruops_quality` (→ `avuruobs_quality`).

  **Upgrading from 0.2.x is not a plain `helm upgrade`.** Chart resource names
  and the `app.kubernetes.io/name` selector label derive from the chart name,
  and selector labels are immutable — an in-place upgrade of a release
  installed as `avuruops` would try to rename every object and fail. Two
  supported paths:
  - *Keep the existing release:* `helm upgrade avuruops
    oci://ghcr.io/<org>/charts/avuruobs --version 0.3.0 --set
    nameOverride=avuruops`, which pins the old name and fullname so no object
    is renamed.
  - *Start clean:* `helm uninstall avuruops` then install as `avuruobs`. The
    ClickHouse PVC is not deleted with the release, so retained telemetry
    survives if you re-point the new release at it; otherwise data starts
    fresh.

  If you set any `AVURUOPS_*` variable yourself (Compose, bare `docker run`,
  your own manifests), rename it — the chart handles its own. Green series
  written before the upgrade carry `avuruops_quality` and therefore read as
  *unknown* quality (the same tier as pre-AEP data), never as measured; they
  age out by retention.

### Fixed

- **A fresh install with demo mode on could end up with no admin account.** The
  admin bootstrap only ran when the install had no users at all, and the demo
  viewer — which the server creates itself, from a sibling goroutine — could be
  written first. The bootstrap then read that as "already provisioned" and
  skipped the admin, on that boot and every boot after: `admin` did not exist,
  so every sign-in attempt failed with `Invalid email or password` even with the
  correct password from the release Secret. The demo viewer no longer counts
  toward that check, so the admin is created whichever write lands first — and
  an install already stuck in this state repairs itself on the next restart.
- **The demo visitor lands on the demo project, not `default`.** A one-click
  demo sign-in now opens on the project the viewer can actually see, and the
  active project is re-validated against the signed-in identity — so a project
  left over from a previous session can no longer stick and produce an empty
  view. `GET /api/v1/projects` is marked `no-store` so one user's project list
  is never served from cache to the next.
- **Helm install could fail on a fresh cluster.** The auth and ingest Secret
  templates indexed into the result of `lookup` before checking it found
  anything, so rendering broke when the Secret did not exist yet — precisely
  the first-install case.
- **Login behind a reverse proxy.** The UI now forwards the client `Host`
  with its port, so sign-in works when the port is not the scheme default.
- **Settings Users tab no longer hides the tab bar.** It is now an in-place tab
  (`?tab=users`) instead of a separate page, so the tab navigation stays put;
  `/settings/users` is kept as a redirect for deep links.
- Login page brand casing ("avuru obs" → "Avuru Obs"), matching every other
  surface.

### Security

- Gateway: pinned `golang.org/x/text` to v0.39.0 (CVE-2026-56852).

## [0.2.0] — 2026-07-28

**Depth and control.** v0.1 proved the wedge — a live service map in under
five minutes with zero app changes. v0.2 makes that install safe to run for
real teams: the hub is **secure by default** (login, roles, per-project
grants, OIDC SSO), signals are **modular** (a traces-only install carries no
log or profile weight), the sensor is **provably safe to leave on**, and four
new modules — error tracking, service health groups, alerting, and green
energy/carbon — turn the data you already collect into triage, status and
accountability. The project is now licensed AGPL-3.0.

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
  components. Per-project ingest keys land next on the same seam
  (AEP `design/2026-07-21-auth-oidc-rbac.md`).
- **Enterprise SSO via OpenID Connect.** Any OIDC IdP works — Keycloak, Entra,
  Okta, Google, Dex (LDAP/AD by federating through the IdP) — and it ships in
  OSS, not behind an enterprise tier. The hub runs the authorization-code +
  PKCE flow itself (`/api/v1/auth/oidc/start` → IdP →
  `/api/v1/auth/oidc/callback`) — no oauth2-proxy, no extra pod — and an SSO
  login ends in the same server-side session as a local one, so revocation
  stays immediate. IdP groups map to per-project grants declaratively
  (`auth.oidc.mapping`: group → role on projects, plus a `defaultRole`
  fallback), applied at **read time** on every request — moving a user between
  IdP groups re-scopes their access on their next request, no re-login.
  `forceSSO` hides the local password form for IdP-only fleets (the local
  admin API login stays available as break-glass). Configured entirely from
  Helm values (`auth.oidc.*`; the client secret comes from your own Secret or
  a chart-managed one, never the config file): the mapping is hot-reloaded
  (~15s, no restart), and IdP discovery is fail-loud at hub startup so a wrong
  issuer stops the rollout instead of shipping a broken login. An opt-in e2e
  profile drives the full flow against a real mock IdP through the compose
  stack (`deploy/compose/docker-compose.oidc-e2e.yaml`).
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
[Unreleased]: https://github.com/avuruvision/avuru-obs/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/avuruvision/avuru-obs/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/avuruvision/avuru-obs/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/avuruvision/avuru-obs/releases/tag/v0.1.0
-->
