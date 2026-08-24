# Testing

Write or update tests **alongside** the implementation, not after.

## The pyramid

1. **Unit** (fast, everywhere)
   - Go: table-driven tests, `go test -race ./...`. Handlers test against a
     fake `storage.Store`; never spin ClickHouse for a handler test.
   - TS: component/unit tests only where logic is non-trivial; the UI safety
     net is Playwright.

2. **Integration** (per component, Dockerized)
   - Go: `testcontainers-go` spins an ephemeral ClickHouse — covers schema
     migrations, the ClickHouse `storage.Store` impl, and OTLP→CH round-trips.
     Run with `cd hub && make test-int`. Path-filtered in CI.
   - OpAMP/API contract: `httptest` against the hub's REST/WS surface.

3. **E2E — Playwright (AI-maintained, SigNoz pattern)**
   - Specs live in `ui/e2e/`. Run against the compose stack with **seeded demo
     data** for determinism.
   - **`make e2e-ui` is the only supported way to run them, and it is a CI
     gate.** It pins the admin password, and `ui/e2e/global-setup.ts` signs in
     once and hands every spec that session — so the suite runs against a real
     auth-enabled hub. `auth.spec.ts` opts out with an empty `storageState`,
     because its subject is the signed-out experience. Do not reach for an
     anonymous-access override: that is what kept this suite out of CI, and
     three specs silently rotted in the meantime.
   - **Seeded data lives for ten minutes.** Fixtures are rebased to `now-5m`
     and the UI's default range is 15m, so re-seed before a run rather than
     debugging an "empty screen" that is really an expired window. Seeding
     twice without truncating DOUBLES the data — MergeTree does not dedupe —
     and shows up as failing counts in specs unrelated to your change.
   - **Counts in specs are a contract with the fixtures.** Add a
     `traces_*.json` and the service counts in `service-map*.spec.ts` move with
     it.
   - Maintained via the `.claude/agents/` trio:
     - `playwright-test-planner` — derives E2E scenarios from a feature spec
     - `playwright-test-generator` — writes specs against the golden screens
       (service map, trace waterfall, log search, flame graph)
     - `playwright-test-healer` — repairs specs after intentional UI changes
   - CI guard: `cd ui && npm run build` must succeed — the static export
     fails on any server-only Next.js feature, by design.

4. **The TTV gate (the product metric as a test)**
   - GitLab CI job: create a `kind` cluster → `helm install avuruobs` → deploy
     the demo app → poll the Hub API → **assert service map nodes/edges,
     traces, and logs are visible within 5 minutes**. A red TTV gate blocks
     release.

## Commands

| What | Command |
|---|---|
| Go unit | `cd hub && go test -race ./...` |
| Go integration | `cd hub && make test-int` |
| UI lint+build guard | `cd ui && npm run lint && npm run build` |
| E2E API (Go: drop-in promise, seeded determinism) | `make e2e` (owns the compose lifecycle) |
| E2E UI (Playwright smoke, specs in `ui/e2e/`) | `make e2e-ui` (compose lifecycle + seeded data) |
| E2E Helm (kind install smoke: traces + correlated logs) | `make e2e-helm` (owns the kind lifecycle) |
| Everything CI runs | `make check` |

## Opt-in: OIDC SSO e2e

The SSO round trip (hub → mock IdP → callback → session → mapped grants) runs
only against the `oidc-e2e` compose profile — the default stacks have no IdP,
and both test gates default OFF, so `make e2e` / `make e2e-ui` are unaffected.

```bash
AVURUOBS_AUTH_ADMIN_PASSWORD=e2e-admin-pw docker compose \
  -f deploy/compose/docker-compose.yaml \
  -f deploy/compose/docker-compose.oidc-e2e.yaml \
  --profile oidc-e2e up -d --build --wait hub mock-oidc
cd e2e && AVURUOBS_E2E_OIDC=1 go test -tags=e2e -count=1 -run OIDC ./...
cd ui  && OIDC_E2E=1 npx playwright test e2e/auth.spec.ts   # login-page SSO assertions
```

Fixtures are pinned across three files that must agree: the mock's claims
(`JSON_CONFIG` in `deploy/compose/docker-compose.yaml`), the hub's mapping
(`deploy/compose/oidc-e2e.yaml`), and the assertions (`e2e/oidc_test.go`).

## Rules

- A bug fix lands with the test that would have caught it.
- Tests must not depend on wall-clock timing of ingestion: poll with deadline
  helpers, never `sleep`.
- Demo/seed data is deterministic and versioned (`deploy/compose/seed/`).
