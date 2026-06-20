# Avuru Obs Development Guide

## What is Avuru Obs?

An open-source, all-in-one observability platform: traces, metrics, logs, and
continuous profiling in a single install. It replaces the Grafana LGTM stack
with one storage engine (ClickHouse) and one UI.

**Core value (the wedge)**: zero-config time-to-value — fresh K8s cluster →
live service map in **under 5 minutes**, with **zero app changes** (eBPF).
Every feature and PR is judged against that metric.

## Architecture (WHAT)

Mixed-language monorepo — monorepo at the VCS level, **polyrepo at the build
level**: each component owns its toolchain; no cross-language build
orchestrator. See [`agent_docs/architecture.md`](agent_docs/architecture.md)
for data flow and decision rationale.

| Path | Language | Purpose |
|---|---|---|
| `agent/` | **Rust** (aya eBPF) | `avuru-agent`: L4 flow tracer feeding the service map; OTLP export |
| `hub/` | **Go** | API-only single binary: REST/WS API, OpAMP server, alerting, storage interface (ClickHouse impl). The UI is a separate deployable. |
| `gateway/` | OCB manifest | Minimal OTel Collector distro + ClickHouse schemas/migrations |
| `ui/` | **Next.js/TS** | Static-export SPA (`output: 'export'`) served by its own nginx image (separate pod), single-origin with the hub |
| `proto/` | protobuf | Shared Rust↔Go↔TS contracts — single source of truth, codegen only |
| `sensor/` | YAML | DaemonSet pod assembly: avuru-agent + OBI + OTel Collector + eBPF profiler |
| `deploy/` | Helm/compose | `helm/` flagship chart + operator; `compose/` all-in-one demo |

**Data flow**: sensor pod (eBPF + OTLP) → gateway collector → ClickHouse;
Hub reads ClickHouse (SQL) and configures agents (OpAMP). The Hub is NEVER in
the telemetry byte-path.

**Reuse over rewrite**: OBI, OTel Collector, and the OTel eBPF profiler are
upstream OSS reused as-is (pinned in
[`agent_docs/tech_stack.md`](agent_docs/tech_stack.md)). We only build what
doesn't exist: the Rust flow tracer, the Hub, and the UI.

## Working on the Codebase (HOW)

**Before starting a task**, read the relevant docs from `agent_docs/`:

- `agent_docs/architecture.md` — data flow, locked decisions + rationale
- `agent_docs/tech_stack.md` — pinned versions and upgrade rules
- `agent_docs/development.md` — dev workflows, ports, common tasks
- `agent_docs/testing.md` — test pyramid, commands per component
- `agent_docs/proto_contracts.md` — shared-contract rules (read before touching `proto/`)
- `agent_docs/go_style.md` / `agent_docs/rust_style.md` / `agent_docs/ui_patterns.md` — read only when actively coding in that language

**Governance & meta** (repo root): `CONTRIBUTING.md` (workflow), `AI_POLICY.md`
(AI use — note: **no AI commit trailers**, this guide is the source of truth),
`SECURITY.md`, `STYLE_GUIDE.md` (→ `agent_docs/*_style.md`), `design/` (specs/
RFCs). CI: `.github/workflows/ci.yml` mirrors `make check` + helm lint.

## Generated files — NEVER hand-edit

- Anything under `*/generated/` or marked `// Code generated` — regenerate via `make proto` *[M1+ — echoes until buf is wired]*
- `hub/internal/ui/dist/` — produced by `make ui` (Next.js static export)
- `gateway/` collector binary — built from the OCB manifest, never patched

## Validation commands (run before claiming done)

| Component | Command |
|---|---|
| hub | `cd hub && go build ./... && go test -race ./... && golangci-lint run` |
| agent | `cd agent && cargo check && cargo test && cargo clippy -- -D warnings` |
| ui | `cd ui && npm run lint && npm run build` (build MUST succeed — static export fails on server-only features) |
| proto | `make proto && git diff --exit-code` (codegen must be committed) *[M1+]* |
| all | `make check` |

`make check` mirrors CI but does **not** run `golangci-lint` — run it via
`cd hub && make lint` (or the hub row above) when you touched Go code.

## Key Principles

1. **The wedge is law**: anything adding install friction or delaying first
   data needs a strong justification.
2. **OTel semantic conventions everywhere** — OTLP in, OTel attributes in
   ClickHouse, no proprietary formats.
3. **Storage behind the interface**: Hub code talks to
   `hub/internal/storage.Store`, never to ClickHouse SQL directly outside the
   ClickHouse implementation package.
4. **Enterprise seam**: auth behind a provider interface; schemas carry a
   `tenant` field; retention is a policy object. Don't bypass these.
5. **Existing patterns**: explore similar files before implementing; keep
   files under ~300 lines.
6. **No sampling by default**: 100% ingestion; sampling is an explicit,
   opt-in gateway knob.

## PR hygiene for agent-generated code

1. **One logical change per MR**, scoped small — even if the agent can produce
   more in one session. Focused MRs review faster and classify accurately.
2. **MR description explains intent (the "why"), not just the diff.**
   Reviewers need the goal to catch a plausible-but-wrong trade-off or an
   agent solving the wrong problem.
3. **Agent-generated branches use the `ai/` prefix** (e.g.
   `ai/add-flow-aggregation`) so reviewers can calibrate scrutiny.
4. **Tests land alongside the implementation, not after** — see
   [`agent_docs/testing.md`](agent_docs/testing.md) for the pyramid and
   per-component commands.
5. **A `proto/` change ships contract + regenerated code + consuming code in
   ONE MR** — never split a contract change across MRs (see
   [`agent_docs/proto_contracts.md`](agent_docs/proto_contracts.md)).

## Git commits

- Branch from `develop`; conventional commits (`feat:`, `fix:`, `docs:`,
  `chore:`...), scope = component (`feat(hub): ...`)
- Commit as the configured git author only — **never add `Co-Authored-By`
  trailers** (no AI co-author attribution in history)
- Run the validation commands above for every component you touched before
  committing; never bypass or skip failing checks

### Branch & push hygiene (a feature branch NEVER tracks `develop`)

Two remotes exist: `github` (avuruvision, where AI dev happens) and `origin`
(GitLab, the company repo). Keep `develop` clean — it is the integration
branch, fed only through reviewed PRs, never by a stray push.

Branch naming: milestone branches use `feature/<milestone>` (e.g.
`feature/m2-deployable-otlp-backend`); smaller ad-hoc agent tasks use
`ai/<topic>`. Both branch from `develop` and follow the same push hygiene.

1. **Create branches with `git switch -c <feature/…|ai/…> develop`.** Don't
   `git branch` off a detached/ambiguous base.
2. **First push sets the branch's OWN upstream: `git push -u github
   <branch>`.** Never let a branch track `github/develop` — a misconfigured
   upstream is how a bare `git push` silently lands work on `develop`
   (post-mortem: M1, 2026-06). Verify with `git branch -vv`: the `[github/...]`
   in brackets must match the branch name, not `develop`.
3. **Never `git push` to `develop` directly** (no `git push github
   HEAD:develop`, no bare `git push` while tracking develop). Integrate via a
   GitHub PR `<branch> → develop`.
4. **`main` is release-only** — never push feature work to it.

## Merge conflict resolution

1. **Never blindly pick a side.** Read both sides of every conflict to
   understand each change's intent before resolving.
2. **Refactor/move conflicts need extra verification.** When one side moved or
   extracted code, diff the discarded side against the destination files —
   code diverges after extraction, and a naive "keep ours" silently drops the
   other branch's fixes.
3. **Never hand-resolve generated code.** Conflicts in `*/generated/` or
   `hub/internal/ui/dist/` are resolved at the source (`proto/`, `ui/`) and
   regenerated via `make proto` / `make ui`.
4. **Verify the result builds** — run the touched component's validation
   command after resolving.
5. **When uncertain, stop and ask** rather than guess. A wrong guess silently
   breaks things; asking is cheaper than debugging later.

## Platform notes

- **macOS**: `cd agent && cargo check && cargo test` works (eBPF is
  `#[cfg]`-gated). Full eBPF build/test requires Linux — use
  `agent/Dockerfile` or CI.
- **Ports** (local defaults): hub 8080, OpAMP 4320, gateway OTLP 4317/4318,
  ClickHouse 8123/9000, Next.js dev 3000 — full table in
  [`agent_docs/development.md`](agent_docs/development.md).
- **UI iteration**: `cd ui && npm run dev` (HMR, proxies `/api` to the hub on
  8080). The hub serves the *last built* export — UI changes don't appear in
  the hub binary until `make ui`.
- **Full stack**: `make dev` (compose: ClickHouse + collector + demo app)
  *[M1+]*.

---

_Need more detail? Check `agent_docs/` or the component's own README/build.md._
