# Testing

Write or update tests **alongside** the implementation, not after.

## The pyramid

1. **Unit** (fast, everywhere)
   - Go: table-driven tests, `go test -race ./...`. Handlers test against a
     fake `storage.Store`; never spin ClickHouse for a handler test.
   - Rust: `cargo test` on pure logic (flow aggregation, OTLP mapping). eBPF
     program logic is factored so the userspace side is testable off-Linux.
   - TS: component/unit tests only where logic is non-trivial; the UI safety
     net is Playwright.

2. **Integration** (per component, Dockerized)
   - Go: `testcontainers-go` spins an ephemeral ClickHouse — covers schema
     migrations, the ClickHouse `storage.Store` impl, and OTLP→CH round-trips.
     Run with `cd hub && make test-int`. Path-filtered in CI.
   - OpAMP/API contract: `httptest` against the hub's REST/WS surface.
   - Rust/eBPF: privileged CI job on a kernel ≥5.8 runner loads the eBPF
     programs and asserts flow capture (DeepFlow's agent-verify pattern).

3. **E2E — Playwright (AI-maintained, SigNoz pattern)**
   - Specs live in `ui/e2e/`. Run against the compose stack with **seeded demo
     data** for determinism.
   - Maintained via the `.claude/agents/` trio:
     - `playwright-test-planner` — derives E2E scenarios from a feature spec
     - `playwright-test-generator` — writes specs against the golden screens
       (service map, trace waterfall, log search, flame graph)
     - `playwright-test-healer` — repairs specs after intentional UI changes
   - CI guard: `cd ui && npm run build` must succeed — the static export
     fails on any server-only Next.js feature, by design.

4. **The TTV gate (the product metric as a test)**
   - GitLab CI job: create a `kind` cluster → `helm install avuruops` → deploy
     the demo app → poll the Hub API → **assert service map nodes/edges,
     traces, and logs are visible within 5 minutes**. A red TTV gate blocks
     release.

## Commands

| What | Command |
|---|---|
| Go unit | `cd hub && go test -race ./...` |
| Go integration | `cd hub && make test-int` |
| Rust | `cd agent && cargo test && cargo clippy -- -D warnings` |
| UI lint+build guard | `cd ui && npm run lint && npm run build` |
| E2E API (Go: drop-in promise, seeded determinism) | `make e2e` (owns the compose lifecycle) |
| E2E UI (Playwright smoke, specs in `ui/e2e/`) | `make e2e-ui` (compose lifecycle + seeded data) |
| E2E Helm (kind install smoke: traces + correlated logs) | `make e2e-helm` (owns the kind lifecycle) |
| Everything CI runs | `make check` |

## Rules

- A bug fix lands with the test that would have caught it.
- Tests must not depend on wall-clock timing of ingestion: poll with deadline
  helpers, never `sleep`.
- Demo/seed data is deterministic and versioned (`deploy/compose/seed/`).
