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
| `hub/` | **Go** | API-only single binary: REST/WS API, OpAMP server, alerting, storage interface (ClickHouse impl). The UI is a separate deployable. |
| `gateway/` | OCB manifest | Minimal OTel Collector distro + ClickHouse schemas/migrations |
| `ui/` | **Next.js/TS** | Static-export SPA (`output: 'export'`) served by its own nginx image (separate pod), single-origin with the hub |
| `proto/` | protobuf | Shared Go↔TS contracts — single source of truth, codegen only |
| `sensor/` | YAML | DaemonSet pod assembly: OBI + OTel Collector + eBPF profiler |
| `deploy/` | Helm/compose | `helm/` flagship chart + operator; `compose/` all-in-one demo |

**Data flow**: sensor pod (eBPF + OTLP) → gateway collector → ClickHouse;
Hub reads ClickHouse (SQL) and configures agents (OpAMP). The Hub is NEVER in
the telemetry byte-path.

**Reuse over rewrite**: OBI, OTel Collector, and the OTel eBPF profiler are
upstream OSS reused as-is (pinned in
[`agent_docs/tech_stack.md`](agent_docs/tech_stack.md)). We only build what
doesn't exist: the Hub and the UI.

## Working on the Codebase (HOW)

**Before starting a task**, read the relevant docs from `agent_docs/`:

- `agent_docs/architecture.md` — data flow, locked decisions + rationale
- `agent_docs/tech_stack.md` — pinned versions and upgrade rules
- `agent_docs/development.md` — dev workflows, ports, common tasks
- `agent_docs/testing.md` — test pyramid, commands per component
- `agent_docs/go_style.md` / `agent_docs/ui_patterns.md` — read only when actively coding in that language

**Governance & meta** (repo root): `CONTRIBUTING.md` (workflow), `GOVERNANCE.md`
(decisions, maintainers), `MAINTAINERS.md`, `AI_POLICY.md` (AI use — note: **no
AI commit trailers**, this guide is the source of truth), `SECURITY.md`,
`STYLE_GUIDE.md` (→ `agent_docs/*_style.md`), `RELEASING.md` + `ROADMAP.md` +
`CHANGELOG.md` (release/direction), `design/` (Avuru Enhancement Proposals). CI:
`.github/workflows/ci.yml` mirrors `make check` + helm lint; `release.yml` cuts
releases on `vX.Y.Z` tags.

## Generated files — NEVER hand-edit

- `hub/internal/ui/dist/` — produced by `make ui` (Next.js static export)
- `gateway/` collector binary — built from the OCB manifest, never patched

## Validation commands (run before claiming done)

| Component | Command |
|---|---|
| hub | `cd hub && go build ./... && go test -race ./... && golangci-lint run` |
| ui | `cd ui && npm run lint && npm run build` (build MUST succeed — static export fails on server-only features) |
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
7. **No competitor names in user-facing text**: describe avuru-obs on its own
   terms. See below.

## No competitor names (user-facing text)

`CHANGELOG.md`, release notes/tag messages, `README.md`, `ROADMAP.md`, UI
strings, and the docs site describe what avuru-obs does — never how it
compares. **"X-style", "X-like", "à la X" count as naming a competitor**, and
`release.yml` copies the changelog section into the GitHub release verbatim,
so a comparison written here ships to users.

- Applies to: Coroot, SkyWalking, Kiali, Datadog, Jaeger, SigNoz, Uptrace,
  Dynatrace, New Relic, Grafana/LGTM as a *comparison* — and any other
  product used as a style reference.
- Not covered: naming an upstream dependency we actually reuse or interop with
  (OTel/OBI/OTLP, "OTLP drop-in for Jaeger endpoints", "a Grafana data source
  is planned", ClickHouse) — that's a fact about our stack, not a comparison.
  Code comments and `agent_docs/` may cite prior art for rationale.
- The **only** place comparisons belong is the docs site's dedicated Compare
  section (see `.claude/skills/docs-align`).

Before shipping user-facing text, sweep it (scoped to shipped prose — UI
source is excluded on purpose: its hits are prior-art comments, which are
allowed and would drown the signal):

```bash
grep -rin "coroot\|skywalking\|kiali\|datadog\|signoz\|uptrace\|dynatrace\|new relic" \
  CHANGELOG.md README.md ROADMAP.md deploy/helm/README.md docs/
```

Then re-read your own new prose for "X-style"/"X-like" — grep won't catch a
comparison phrased without the product name.

> v0.1.0 shipped with "Coroot-style" and "SkyWalking-style" in its release
> notes and had to be scrubbed after the fact — the changelog is user-facing.

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

## Git commits

- Branch from `main`; conventional commits (`feat:`, `fix:`, `docs:`,
  `chore:`...), scope = component (`feat(hub): ...`)
- **Sign your commits** — signing is required (`COMMIT-SIGNING-SETUP.md`);
  `main` and `vX.Y` branches enforce it via branch protection
- Commit as the configured git author only — **never add `Co-Authored-By`
  trailers** (no AI co-author attribution in history)
- Run the validation commands above for every component you touched before
  committing; never bypass or skip failing checks

### Branch & push hygiene (a feature branch NEVER tracks `main`)

`main` is the single development **trunk** (Kiali-style, see `RELEASING.md`).
Two remotes exist: `github` (avuruvision, where AI dev happens) and `origin`
(GitLab, the company repo). Keep `main` clean — it is fed only through reviewed
PRs, never by a stray push.

Branch naming: milestone branches use `feature/<milestone>` (e.g.
`feature/m2-deployable-otlp-backend`); smaller ad-hoc agent tasks use
`ai/<topic>`. Both branch from `main` and follow the same push hygiene.

1. **Create branches with `git switch -c <feature/…|ai/…> main`.** Don't
   `git branch` off a detached/ambiguous base.
2. **First push sets the branch's OWN upstream: `git push -u github
   <branch>`.** Never let a branch track `github/main` — a misconfigured
   upstream is how a bare `git push` silently lands work on `main`
   (post-mortem: M1, 2026-06). Verify with `git branch -vv`: the `[github/...]`
   in brackets must match the branch name, not `main`.
3. **Never `git push` to `main` directly** (no `git push github HEAD:main`, no
   bare `git push` while tracking main). Integrate via a GitHub PR
   `<branch> → main`.
4. **`vX.Y` release branches are release-only** — patch backports land there via
   PR (see `RELEASING.md`), never day-to-day feature work.

## Merge conflict resolution

1. **Never blindly pick a side.** Read both sides of every conflict to
   understand each change's intent before resolving.
2. **Refactor/move conflicts need extra verification.** When one side moved or
   extracted code, diff the discarded side against the destination files —
   code diverges after extraction, and a naive "keep ours" silently drops the
   other branch's fixes.
3. **Never hand-resolve generated code.** Conflicts in `*/generated/` or
   `hub/internal/ui/dist/` are resolved at the source (`ui/`) and
   regenerated via `make ui`.
4. **Verify the result builds** — run the touched component's validation
   command after resolving.
5. **When uncertain, stop and ask** rather than guess. A wrong guess silently
   breaks things; asking is cheaper than debugging later.

## Platform notes

- **Ports** (local defaults): hub 8080, OpAMP 4320, gateway OTLP 4317/4318,
  ClickHouse 8123/9000, Next.js dev 3000 — full table in
  [`agent_docs/development.md`](agent_docs/development.md).
- **UI iteration**: `cd ui && npm run dev` (HMR, proxies `/api` to the hub on
  8080). The hub serves the *last built* export — UI changes don't appear in
  the hub binary until `make ui`.
- **Full stack**: `make dev` (compose: ClickHouse + collector + demo app).

---

_Need more detail? Check `agent_docs/` or the component's own README/build.md._
