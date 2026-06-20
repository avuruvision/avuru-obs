# Avuru Obs — Product & Architecture Design

Date: 2026-06-12
Status: Approved (brainstorm + 5 research agents, June 2026)

## 1. Product thesis

**One-liner:** observability that's live in 5 minutes — traces, metrics,
logs, profiling, zero code changes.

- **Wedge:** zero-config time-to-value. North-star metric: *fresh K8s cluster
  → live service map in < 5 minutes, no app changes*. Measured in CI.
- **Business model:** Apache 2.0 OSS day 1 → open-core commercial later
  (hosted SaaS + enterprise features). The proven path of SigNoz, Coroot,
  HyperDX (acquired by ClickHouse). Enterprise seam designed in from the
  start: auth provider interface, tenancy column in every schema, retention
  policy objects.
- **Positioning:** Coroot's instant-map flow + DeepFlow's zero-code ambition
  + HyperDX's ClickHouse pragmatism — competing on the first five minutes,
  not on feature count. The market (SigNoz, Coroot, HyperDX, Uptrace,
  OpenObserve) is crowded; a "me too" all-in-one loses. Install friction is
  the obsession.

## 2. Scope — v0.1 (depth-tiered)

| Epic | Depth |
|---|---|
| One-command install (Helm flagship + compose sandbox w/ demo app) | Full |
| Zero-code instrumentation (OBI: HTTP/gRPC/SQL/Redis/Kafka; OTLP ingest for SDK apps) | Full |
| Service map + RED metrics (eBPF flows, drill-down) | Full |
| Trace explorer (waterfall, search; eBPF + SDK unified) | Full |
| Logs (stdout/stderr collect, full-text search, trace_id correlation) | Basic |
| Continuous profiling (CPU flame graphs per service) | Lite |
| Infra metrics (kubeletstats node/pod) | Supporting |
| Onboarding UX ("first 5 minutes" flow, teaching empty states, preflight) | Full |

**v0.2:** alerting + channels, dashboards, AI troubleshooting (NL search,
anomaly explanation), OIDC SSO, log pipelines.
**v0.3:** trace↔profile correlation, multi-cluster, retention tiers, usage
metering.

## 3. Architecture

Assemble upstream OSS; build only what doesn't exist (the Rust flow agent,
the Hub, the UI). OTLP and OTel semantic conventions end-to-end.

See `agent_docs/architecture.md` for the living version (diagram, data flow,
constraints). Summary of the locked decisions:

| Decision | Choice | Key evidence (June 2026 research) |
|---|---|---|
| Storage | ClickHouse, single store for 4 signals + flows, behind a Go `Store` interface | Only engine with production-proven profiles storage (Coroot's stack-dedup schema, qryn); Apache 2.0 decade-stable; official beta OTel exporter; SigNoz/ClickStack/Coroot are existence proofs. Runner-up GreptimeDB (1.0 GA 04/2026) — re-evaluate mid-2027 |
| Zero-code agent | OBI (ex-Grafana Beyla), reused as-is as sensor-pod sibling container, version-pinned | Apache 2.0/CNCF, OTLP-native, protocol coverage incl. Kafka/gRPC; v0.9 beta, GA expected late 2026. DeepFlow agent can't emit OTLP standalone; Pixie too heavy; Coroot agent lacks gRPC/Kafka |
| Service map | Own **Rust** eBPF L4 flow tracer (aya), OTLP out, map independent of traces | Coroot's proven <5-min mechanic (TCP connect/listen tracing); de-risks wedge from OBI pre-1.0 stitching limits (TLS path breaks across L7 proxies; kernel ≥5.17 for full stitching) |
| Profiling | OTel eBPF profiler (Elastic donation) as Collector receiver, behind an ingestion adapter | OTLP Profiles signal is **alpha** (03/2026, "no production-ready backends" per OTel) — adapter isolates wire-format breaks |
| Hub | Single Go binary (API + OpAMP server + alerting + embedded UI); SQLite default, Postgres for HA; out of the telemetry byte-path | SigNoz publicly retreated from microservices to one binary (v0.76); Coroot/Grafana same; MongoDB is HyperDX's acknowledged debt — avoided |
| Config plane | OpAMP remote-config from the Hub (one URL for agents); Helm/operator for day-0 | ClickStack and SigNoz ship OpAMP in production; spec is beta but remote-config scope is proven |
| UI | Next.js `output:'export'` static SPA, `go:embed` in hub | Unanimous embed pattern (Coroot Vue/go:embed, SigNoz, Grafana); HyperDX's static-export mode proves Next.js works for heavy o11y UIs |
| Sampling | 100% ingestion default; first knob = gateway tail sampling (errors + slow + N% baseline) | Every studied platform defaults to no sampling |
| Pipeline | DaemonSet sensor + singleton cluster-agent + minimal OCB collector gateway | Cluster-agent avoids duplicate cluster metrics; no lighter production-proven gateway exists (Fluent Bit lacks tail sampling; Vector = Datadog dependency); watch otel-arrow Phase 2 |
| Stack | Rust for the node agent (footprint/safety, DeepFlow's choice); Go for hub/gateway/operator reusing OTel Go OSS (opamp-go, collector, clickhouse-go); TS/Next.js UI | Per user directive: agent in Rust, rest in Go, maximize reuse of existing Go OTel libraries |
| Repo | DeepFlow-shaped monorepo (VCS-level mono, build-level poly, path-filtered CI) + HyperDX AGENTS.md/agent_docs AI-dev system + SigNoz `.claude/agents` Playwright trio | DeepFlow is the only proven Rust-agent+Go-server monorepo; HyperDX has best-in-class agent docs |

## 4. Risks

1. **OBI pre-1.0 churn** — pin versions; document stitching limits honestly;
   the map does not depend on traces by design.
2. **OTLP Profiles alpha** — ingestion adapter; version-pin the collector.
3. **ClickHouse single-node footprint** (~4 GB floor / 8 GB recommended) —
   ship tuned config; `Store` interface keeps a GreptimeDB swap open.
4. **Crowded market** — the TTV gate in CI keeps every milestone honest
   against the wedge.

## 5. Verification

The TTV metric is a CI test: kind cluster → `helm install` → demo app →
assert map/traces/logs visible via Hub API within 5 minutes. Full test
strategy: `agent_docs/testing.md`.

## 6. Delivery plan

M0 AI-dev foundation (docs before code) → M1 walking skeleton
(OTLP→CH→Hub→UI) → M2 zero-code path (OBI + Rust flow tracer + live map) →
M3 logs + infra metrics → M4 profiling lite + install polish → M5 hardening
(OpAMP, auth, TTV gate) → v0.1 release.

## Appendix: research provenance

Five research agents (June 2026): storage engines (ClickHouse vs
Victoria stack vs GreptimeDB vs Quickwit/Doris/OpenObserve), eBPF agents
(OBI vs Coroot vs DeepFlow vs Pixie vs Odigos + profilers), competitor
control-plane architectures (Coroot/ClickStack/SigNoz/collector patterns),
UI packaging (6 platforms), and repo structures (5 monorepos). Full reports
in the planning session; key citations inline in `agent_docs/architecture.md`
and `agent_docs/tech_stack.md`.
