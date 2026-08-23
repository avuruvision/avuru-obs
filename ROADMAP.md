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

## v0.4 — administration, and installs that heal themselves — ALL SHIPPED (v0.4.0)

v0.4 was not the release this file predicted. The queued items below kept their
AEPs and moved to v0.5/v0.6; what shipped instead came out of *running* the
product — the account lifecycle the Users tab was missing, and a set of failures
that only appear on a real cluster. Full detail in [CHANGELOG.md](CHANGELOG.md).

| Theme | Shipped |
|---|---|
| **User management, end to end** | Settings → Users edits names and role grants, resets passwords (signing every session of that user out) and **deletes** users behind an explicit disable-first step; a new Settings → Account tab lets any local user change their own password. Password operations are refused for SSO users — the identity provider owns that credential — [AEP](design/2026-08-06-users-crud-password.md) |
| **Three authentication holes closed** | An admin could mint a *working* local password for an SSO-only account, bypassing the IdP's MFA and conditional access; an attacker rotating IP addresses bypassed the login lockout entirely (both axes keyed on the client IP, so N addresses bought N × 5 attempts against one account); and an SSO login could take over a local account's email and break its login, since `auth_user` has no unique index and the password lookup had no `ORDER BY` |
| **A schema the migrate hook never applied now heals itself** | Helm runs `post-install` hooks only *after* `--wait` succeeds, so a release that timed out on any component never created the migrate Job — while the Deployments rolled out normally, leaving a cluster that looked healthy and answered `Unknown table expression identifier` to everything. The hub now applies missing migrations itself (`hub.autoMigrate`), logs **one** actionable ERROR when it can't, and Settings → Status gained a **Schema** component |
| **Installs that could never have worked** | `clickhouse.external.database` was documented and schema-checked but every migration hardcoded `otel.`; the chart's image defaults pointed at Docker Hub while releases publish to GHCR, with a tag the release workflow never pushed; green TDP estimation shipped with no image at all; a node without RAPL took the whole sensor DaemonSet into CrashLoopBackOff, dropping logs, traces and metrics along with an optional energy signal |
| **Reverse-proxy logins** | A proxy that rewrites `Host` turned every write — the login POST first — into `cross-origin request rejected`. `auth.trustedOrigins` and `auth.originCheck` fix it without loosening the strict default |

## v0.5 — operate it from the UI — ALL SHIPPED (v0.5.0)

Everything below shipped in v0.5.0; the full detail lives in
[CHANGELOG.md](CHANGELOG.md) and the linked AEPs.

| Theme | Shipped |
|---|---|
| **Runtime collection control — completion** | The real applier (sensor ConfigMap patches, rollout via a hub-owned checksum annotation) and the editable Settings → Collection UI, behind the default-off flag — per-signal switches the sensor follows in seconds, no `helm upgrade` — [AEP](design/2026-07-27-collection-control-plane.md) |
| **Service groups from the UI** | Health groups and criticality tiers authored in the app; chart-declared groups stay read-only, auto-discovery keeps working — [AEP](design/2026-08-07-service-groups-crud.md) |
| **Storage & Access tabs** | Per-signal usage, compression, TTL and cluster topology made visible (the connection stays chart-owned); RBAC legible — the permissions matrix, the OIDC group→role mapping as an editable overlay |
| **Personal API tokens** | `Authorization: Bearer avurut_…` for scripts and CI — hashed at rest, shown once, resolving to the owner's live permissions — [AEP](design/2026-08-13-api-tokens.md) |
| **Dashboard & service-map restyle** | One overview screen (service summaries by group, live topology, capacity, active alerts) and a map that says *what* is wrong — status rings from the health rollup, real edge latency, URL-state filters. Nodes gained sort/filter |

## v0.6 — open at both ends — SHIPPED (v0.6.0)

The product observed one cluster well and spoke one protocol. Estates are
bigger than that: fleets arrive with senders already deployed, and run more
than one cluster. v0.6 opened both ends of the pipe.

> **As a platform team we adopt avuru-obs without abandoning what we run
> today — existing senders land unchanged, one screen spans our clusters — and
> we leave the same way we came, dual-writing on the way in or out.**

The three themes below shipped in v0.6.0; the full detail lives in
[CHANGELOG.md](CHANGELOG.md) and the linked AEPs. The client surfaces,
declared service metadata and richer auto-tagging did not make the release and
carry over to v0.7 with their AEPs intact; the inter-zone spike ended exactly
as its gate allowed — the AEP landed, the feature moved.

| Theme | Shipped |
|---|---|
| **Wider ingest compatibility** | The release-defining item: Jaeger (gRPC + thrift-HTTP), Zipkin, Prometheus remote-write and Loki push receivers beside OTLP, each values-flag opt-in and default off, every one through the same tenant stage so ingest keys are enforced whatever the protocol; forwarding exporters (OTLP/Kafka) dual-write to another backend during a migration, behind a bounded queue. The drop-in claim is now CI-enforced like the north star — real per-protocol fixtures through chart-rendered receivers on a kind install — [AEP](design/2026-07-27-wider-ingest-compat.md) |
| **Projects completion (Phase 3)** | **Member projects**: one project reads the union of several clusters on every screen, one level deep, granted per member and refused for the writes that need a single tenant. Plus **per-project retention** (an hourly tenant-scoped trim, since a shared table's TTL cannot select one tenant), **per-project storage usage** in Settings → Storage, and **chart component toggles** so a secondary cluster installs the ingest half alone against the central store — with the combinations that cannot work refused at `helm template` time — [AEP](design/2026-07-27-projects-completion.md) |
| **Green follow-through** | Per-node energy in the coverage panel (measured/estimated kept split per row), carbon budgets that say whether they can actually reach anyone and warn when they target a group nothing rolls up to, and the [real-RAPL validation runbook](docs/runbooks/green-rapl-validation.md) the two green AEPs were missing |

## v0.7 — the clients and the labels (directional)

v0.6 opened the ingest side; the data is only as useful as the surfaces that
read it and the words it is filed under. Carried over from v0.6, unchanged in
intent:

- **A map you can trust:** shipped early in the line, because a dependency
  graph that asserts relationships which do not exist is worse than one that
  omits them. Service-mesh proxies and ingress gateways are classified as
  transport and hidden by default, so a meshed cluster stops seeing every
  `app → proxy → app` hop drawn as two application dependencies; edges derived
  from kernel flows are counted and drawn apart from traced calls. Collapsing
  the hop — recovering the real `app → app` dependency across a proxy — needs
  per-trace ancestry and is the open half. See the
  [AEP](design/2026-08-23-service-map-transport.md).
- **More clients:** a **CLI** (`--fail-on` for CI gates) riding v0.5's API
  tokens, and Grafana reading the Hub API through its JSON data sources — a
  documented recipe with example dashboards; a custom plugin only if the
  recipe's limits bite. See the [AEP](design/2026-07-27-clients-grafana-cli.md).
- **Declared service metadata:** direct-OTLP services declare their own
  domain/environment/tier as resource attributes instead of collapsing into
  `(unlabeled)` — group identity grows up to (domain, environment). Its AEP
  lands with the feature branch.
- **Richer auto-tagging:** map Kubernetes labels/annotations to business tags
  and filter by them across every signal. See the
  [AEP](design/2026-07-27-auto-tagging.md).
- **Inter-zone traffic accounting:** cross-zone bytes per service pair from OBI
  network flows. The v0.6 spike settled the question the gate asked — the
  design is written and the implementation is what remains. See the
  [AEP](design/2026-08-18-inter-zone-traffic.md).

## Beyond v0.7

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
