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

## v0.7 — the clients and the labels — SHIPPED (v0.7.0)

v0.6 opened both ends of the pipe. The data that arrived was only as useful as
the words it was filed under and the surfaces that could read it — and, it
turned out, as truthful as the map drawing it.

> **As a platform team we slice telemetry by the vocabulary our org already
> uses, read it from wherever we work, and trust what the map shows us.**

The themes below shipped in v0.7.0; the full detail lives in
[CHANGELOG.md](CHANGELOG.md) and the linked AEPs.

| Theme | Shipped |
|---|---|
| **A map you can trust** | A mesh proxy is a hop, not a dependency. The hub now recognises transport workloads (mesh sidecars, waypoint and ztunnel proxies, ingress and egress gateways) and the map hides them behind a **Show mesh & gateways** toggle, with a `topology` config to correct a misclassification without waiting for a release. Flow-derived edges are drawn and counted apart from traced calls instead of claiming "0 rpm" — [AEP](design/2026-08-23-service-map-transport.md) |
| **The words your org already uses** | **Business tags**: map a Kubernetes label once and it rides every signal as `avuru.tag.<key>`, applied at collection so uninstrumented workloads are tagged too, then offered as a filter on traces and logs. And **declared service metadata**: a service states its own domain, environment and tier as resource attributes, so a board groups by capability across namespaces with no hub config — with a warning when a declaration cannot be honoured — [AEPs](design/2026-07-27-auto-tagging.md), [2](design/2026-07-28-declared-service-metadata.md) |
| **More clients** | The **`avuruobs` CLI** — `services`, `health`, `traces`, `logs`, `status`, and a `--fail-on` predicate for CI gates with three exit codes so a tripped gate is distinguishable from a broken one — and a **Grafana data source**, backend-side so the API token never reaches a browser and queries leave the Grafana server. Plus a `docs ↗` on every screen — [AEP](design/2026-07-27-clients-grafana-cli.md) |
| **Inter-zone traffic accounting** | Bytes per availability-zone pair from kernel flows, standalone from the per-edge feature so the cloud bill can be explained at zone-pair cardinality. Proven on a two-node cluster in CI — [AEP](design/2026-08-18-inter-zone-traffic.md) |
| **A sensor config the sensor could load** | Turning on network flows had been rendering a config the eBPF tracer refuses to parse, so the container never started; two more keys in the same block did not exist at all, leaving TCP-stats collection inert and the documented cardinality bound unapplied. Rendered configs are now parsed in the chart's own tests |

## v0.8 — the map grows up — SHIPPED (v0.8.0)

The map has been the product's front page since v0.1, and a graph of circles for
just as long. v0.8 taught it to say more — with **no new collection**: everything
below is drawn from telemetry that was already arriving, or from a label the hub
already queried for another screen.

| Theme | Shipped |
|---|---|
| **The dependencies that never report** | Databases, caches and message brokers as first-class nodes, derived from the exit spans of the services calling them — no new agent, no new table, nothing to switch on. A broker is drawn from both ends, so a queue is never a dead end. On a zero-code install the eBPF sensor reads the SQL and Redis wire protocols in the kernel, so the database is on the map inside the wedge's five minutes — [AEP](design/2026-08-23-virtual-targets.md) |
| **A map that carries more meaning** | Namespace and service-group **boundaries**; **edge volume** on every edge, on demand; **undetected peers** — the far end of a connection nobody instrumented, which the renderer used to discard outright; a legend that explains every channel in use, and a zoom readout. The docs site gained the page the in-app `docs ↗` link had been pointing past. Two layout defects fell out of proving it on a real cluster: a first layout that lined disconnected components up on a diagonal, and tiled labels that stacked on each other — [AEP](design/2026-08-24-map-encoding.md) |
| **Navigation that grows with the product** | The sidebar is grouped by the question each screen answers — Topology, Signals, Operations, Infrastructure — instead of one ever-growing "Observe" list. The wedge's first click is unchanged, and a layer whose every screen belongs to a module you don't run disappears rather than labelling a gap |
| **The golden screens are gated** | The Playwright suite could only be run by hand, against a stack with authentication weakened — so it never ran, and three specs had rotted across two releases. It now runs against a real authenticated hub, unattended, on every pull request |

## v0.9 — the mesh and the kernel — SHIPPED (v0.9.0)

v0.8 stopped the map *lying* about a meshed cluster. It did not yet tell the
whole truth about one: with the proxies hidden, the false dependencies were
gone and the real ones behind them were still missing. And the kernel had been
giving us less than it could — partly because we were pinned to an upstream
that could not yet give more.

> **As a platform team we can read a meshed cluster: the dependencies our
> services actually have, the fabric that carries them, the link-level faults
> the kernel sees — and whether anything is serving at all when nobody is
> calling.**

| Theme | Shipped |
|---|---|
| **The dependency behind the proxy** | The release-defining item: the hub walks each trace's own parent chain across up to three consecutive transport spans and reports the `app → app` dependency underneath. Per-trace ancestry is what makes it safe — pairing a proxy's inbound edges with its outbound ones in aggregate invents an N×M cross-product of calls nobody made, which is why v0.7 shipped the hiding and not the collapsing. The **Show mesh & gateways** toggle now swaps representations rather than stacking them, so the same request is never drawn twice — [AEP](design/2026-08-25-transport-hop-collapse.md) |
| **The kernel, upgraded** | The eBPF sensor moves to **OBI v0.12.2**, every rendered config key re-verified against that tag's source first. **Retransmits** join RTT and failed connections on the map's edges — a link can lose packets and still measure fast, which is the fault RTT alone hides — closing the "OBI gap" [network health](design/2026-07-19-network-health.md) carried since v0.2. TCP-stats features are named one by one so the bump could not switch the per-syscall `stats_tcp_io` on behind an install's back |
| **Per-edge attribution, proven** | The service map's flow and TCP-stats joins depend on `k8s.src.owner.name` / `k8s.dst.owner.name`, and nothing had ever checked a real OBI on a real kernel produces them — the AEP listed it as blocking production use since v0.2. The kind gate now installs with kernel flows on and asserts it. Running that found why the caveat was worth keeping: TCP stats attach a tracepoint needing debugfs, the sensor mounted neither, and OBI **exits** rather than skipping a feature it cannot start — so an optional metric had been taking zero-code traces and network flows down with it for anyone who enabled it |
| **Mesh-facing surfaces** | A screen for the fabric itself: every proxy's rate, success rate and latency, with the calls it carried **in and out counted apart** — traffic arriving with none leaving is a proxy that stopped forwarding, a failure its own error rate need not show. Plus **control-plane health**, including the configuration your proxies *refused*: a rejected push means control plane and data plane disagree while the fleet keeps serving what it last accepted. Scraped from the gateway, because istiod is one Deployment and a DaemonSet would multiply every series by node count — [AEP](design/2026-08-25-mesh-surfaces.md) |
| **Endpoint checks** | Health when nothing is calling. A group with no spans is either idle or dead, and only a probe tells the two apart at 3 a.m. Two consecutive failures move a group, never one. Each probe emits a span of its own — a check is synthetic traffic, not a side channel — so a failing check links straight to the trace of the request that failed, with the hub sending it as an OTLP *client* of the gateway and never writing `otel_traces` itself — [AEP](design/2026-07-20-endpoint-checks.md) |

## v0.10 — what it costs — SHIPPED (v0.10.0)

Every release so far has answered *what is happening*. v0.9 finished that
story for a meshed cluster: what depends on what, what carries it, and what the
kernel sees breaking underneath. v0.10 answers a different question — the one a
platform team gets from someone who will never open a service map.

> **As a platform team we can say what this cluster is costing, and how much of
> that is buying nothing at all — in the same screens, from the same
> collection, with nothing leaving the cluster to find out.**

| Theme | Shipped |
|---|---|
| **The capacity nobody drew on** | The release-defining item: every workload's reserved CPU and memory against what it actually used, ranked by the gap — and a workload that declares **no** request called out as its own state rather than shown as a zero, because the scheduler cannot place it deliberately and the kubelet evicts it first. Idle is measured against the **peak**, never the mean: a request cannot be cut below what a workload reached without risking eviction the next time it gets there. Prices are chart values; there is no pricing API, because it would be the first outbound call in a product whose promise is that nothing leaves the cluster. No new workload either — the collector already in the sensor carries both the cluster-object receiver and the leader-election Lease that keeps exactly one node reporting, so a cluster fact read from a DaemonSet cannot multiply by the size of the fleet — [AEP](design/2026-08-26-cost-and-waste.md) |
| **A gateway you named anything at all** | Transport classification could only read names, and its built-in list is deliberately narrow because a false positive erases a real service from the map — so a gateway called `public-edge` had its hops drawn as dependencies until somebody noticed. The sensor now carries the labels a mesh writes on its own data plane. Labels only ever **promote**: a sidecar is a container inside the application's pod wearing the application's labels, so there is nothing to read and absence proves nothing. Names remain the answer there, and the operator's `applications` list still beats both — [AEP](design/2026-08-26-transport-from-labels.md) |
| **Why the control plane is silent** | "Not observed" covered three problems with three different fixes: nothing is scraping, the target is not answering, or it answered with metrics this product cannot read. They are told apart now, from Prometheus's scrape report — which bypasses the metric keep-list, so it was already in the tables. The third state is the one worth naming: the control-plane view is **Istio-shaped**, and an operator running a different mesh learns that from the screen instead of an empty card, with the proxy half explicitly unaffected — [AEP](design/2026-08-26-control-plane-diagnosis.md) |
| **Settings is administration** | A read-only account — the shared demo above all — was offered the group editor, Storage and Status: an editor whose every control was already hidden from it, and two tabs whose only endpoint refused it and rendered "couldn't reach the hub". The same gate restored administration on installs running *without* authentication, where it had been refusing what the hub allows |

**Reading a second control plane** was on this list and is not in it. Linkerd's
destination controller publishes no counterpart to *configuration the proxies
refused* — the most valuable number on the card — so it is not a mapping table
but a design question that needs someone running one to answer. The product
states the limitation instead; the AEP records what is missing.

## v0.11 — what was already in your traces — SHIPPED (v0.11.0)

v0.10 answered what a cluster costs, and it needed new collection to do it.
v0.11 adds **none at all**. Every feature in it reads spans the wedge has been
storing since the first five minutes, and asks them questions the product could
not previously ask — which means an install that upgrades sees its history, not
just what arrives next.

> **As a team we can see how our traffic is distributed, what one service is
> doing, what shape a single request took, and what our applications are
> spending on models — from data we were already sending, with nothing new to
> install.**

| Theme | Shipped |
|---|---|
| **The model calls you were already sending** | The release-defining item: applications are calling models, those calls arrive as ordinary spans, and there was no way to ask what any of it added up to. A new **AI** module reports per model — calls, tokens in and out, latency, failures, and how often an answer was cut off at the token ceiling — and per calling service, the same numbers with an owner. Four readings guard the ways of being confidently wrong: the model that **answered** wins over the one requested; **both** token spellings the convention has had are read, because a large share of real traffic still reports the older pair; a call that reported no usage is counted and excluded from the token totals rather than averaged in as a zero; and truncation is not failure — the call succeeded and hit the ceiling, which is the commonest reason a response comes back unusable. Prices are yours to declare and absent by default, an unpriced model is named rather than costed at zero, and there is no pricing API — it would be the first outbound call in a product whose promise is that nothing leaves the cluster — [AEP](design/2026-08-27-ai-observability.md) |
| **A decision about prompts** | The half that made the above an AEP rather than a feature. The gateway had no redaction stage, `otel_traces` stored span and event attributes verbatim under the ordinary retention, and the trace view rendered both to anyone holding Viewer — so on any install whose SDK captured message content, user text was being stored and displayed. No feature put it there and no feature would have shown you. It is now dropped at the gateway **by default**, ungated by the AI module (content arrives whether or not you run the screen), with the pattern anchored so a token *count* is never mistaken for a prompt and a span event keeping its name while losing its attributes. The screen never renders content and reports it when it arrives anyway |
| **Where the traffic actually goes** | Every trace surface returned rows — which requests, how slow — and none answered *how much of what*. A **Breakdown** tab draws the distribution as a treemap and a donut, grouped by service, operation, outcome, span kind or any span/resource attribute, weighted by request count **or** total wall time, because a rare slow operation and a frequent fast one rank identically in one and nothing alike in the other. The tail is a real bucket computed before the limit, so the parts sum to the whole rather than a top-N quietly redrawing itself as the estate — [AEP](design/2026-08-27-trace-analytics.md) |
| **A page for one service, and the shape of one request** | Clicking a service used to open a filtered trace list; there is now a page with its health, its rate/errors/latency, **who calls it and what it depends on** as two separate lists, and its traces, logs and errors behind tabs. And a **Path** view on the trace: the service-level graph of a single request, weighted by the time spent *inside* each service rather than by span duration, with the dependencies that never sent a span of their own drawn as the terminal hops they are |
| **Refused, a third answer to "did it work?"** | A server replying 4xx has neither failed nor succeeded, and the product had only those two words — so a blocked request or an authorization refusal was reported as `OK`. Server-side 4xx is now its own class across the span badge, the operations overview, the trace table and the search filter, deliberately **out of** the error rate: folding it in would put every auth challenge and every crawler 404 into the number people are paged on |
| **A map that says what it means** | An application is a hexagon and the datastore it depends on is a barrel, so the glyph a reader meets most often is the distinctive one; and the trace list shows the status code a span answered instead of the word "OK" it used to contradict one panel over |

## v0.12 — the spend you can act on — IN PROGRESS

v0.11 taught the product to read the model calls already in its trace store,
and to report them. A report is not an action. v0.12 is about the three things
standing between the two — and it opens by fixing a way of reading those spans
that turned out to be wrong.

> **As a team we can see the shape of what our agents actually do, get told
> when spend crosses a line we set, and write down what things cost once.**

| Theme | Planned |
|---|---|
| **Tool calls are not model calls** | The release opens with a defect. The AI module decides what counts as a model call by testing that `gen_ai.operation.name` is *present*, never looking at its value — but `execute_tool`, `invoke_agent` and `create_agent` are legal values of that attribute too, so on an agent workload every tool execution is counted as a call to a model. Call counts inflate, latency quantiles mix a database lookup with a completion, the model resolves to nothing, and the "reported no usage" bucket — which exists to name an instrumentation gap honestly — fills with spans that were never model calls at all. Splitting the population by operation class restores all four — [AEP](design/2026-08-30-agents-budgets-and-rates.md) |
| **An agent turn is a shape** | Once tools are told apart they are worth drawing. An agent turn is a small graph — a model call that decides, a fan-out to tools, results coming back, often another model call after — and the questions worth asking about it are graph questions: which tool is slow, which fails, how many hops before it converges, which one a retry loop is stuck on. The renderer already exists: the Path view weights each node by the time spent *inside* it, which is exactly right when the model-call span contains its tool spans. A tool a turn hit four times is one node with a count, because the loop is the thing worth seeing |
| **A threshold on spend** | Nobody watches a screen. Green already solved this shape for carbon, and solved it as a *pure* state machine writing into the shared alert tables behind a rule-key prefix, with the alerting package unedited by contract. Spend wants that machine with the unit changed — tokens or money, per calling service or across the estate — not a second one, because two state machines writing one table is how firing and pending semantics drift apart. A cost budget over models you have not priced is refused when the config is parsed, since a budget that measures against a floor comes in under every threshold by being ignorant of half the spend |
| **One rate table, written down once** | There are two now, and they differ in more than content: AI prices are a hot-reloading ConfigMap validated fail-loud, cost rates are three environment variables read once at startup. The same operator declaring what their estate costs does it twice, in two formats, one of which needs a pod restart — and the two currency fields can disagree with nothing noticing. Both move behind one resolver, authored in the UI over the overlay storage runtime collection control already established, with chart-declared values still readable and marked read-only the way service groups are |

**Deliberately not in this list**, each for a reason that already exists in the
tree. **Cost joined to green** stays blocked on the real-RAPL confirmation:
green's Kepler read has been unverified on real hardware since v0.2, and
joining it to cost's CI-proven numbers would launder that doubt rather than
resolve it. **Reading a second control plane** still needs an operator running
one. And **root-cause summaries from a model** remain out, because they would
be the first outbound network call in a product whose whole promise is that
nothing leaves the cluster — a release-level decision, not a corner of one.

## Beyond v0.12 (directional)

- **Read a second control plane**, once someone running one can say which of its
  signals answer the four questions the Istio card answers — and which of them
  simply have no answer there.
- **Cost joined to green:** the same reserved-and-idle capacity in Wh and
  gCO2e, on an install running both. One story about waste, in two units.
- **The incident.** Root-cause summaries, with a provider switch that is
  **disabled by default**. Deliberately still out: it would be the first
  outbound network call in a product whose whole promise is that nothing leaves
  the cluster, and that is a release-level decision, not a corner of one — the
  same reason v0.11's AI module prices from rates you declare rather than from
  a pricing API.
- **Scripted multi-step check journeys**, if demand appears — v0.9 ships
  single-request checks deliberately.
- **Deeper profiling:** off-CPU and memory profiles as the upstream OTel eBPF
  profiler grows them.
- **Storage re-evaluation:** ClickHouse stays behind `storage.Store`; GreptimeDB
  is slated for re-evaluation mid-2027 without changing Hub code.

## How this roadmap changes

Open an issue or a [discussion](CONTRIBUTING.md) to propose a change of
direction; open an [AEP](design/README.md) for anything that adds or alters a
[locked decision](agent_docs/architecture.md#locked-decisions-and-rationale).
Roadmap edits go through a normal PR.
