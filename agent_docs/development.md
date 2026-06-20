# Development Workflows

## Repo model

Monorepo at the VCS level, **polyrepo at the build level**. Each component is
a self-contained build root (own `Cargo.toml` / `go.mod` / `package.json` /
Dockerfile). The root `Makefile` is a thin dispatcher only — it must never
contain build logic.

```bash
make hub      # build hub (Go)
make agent    # check/build agent (Rust)
make ui       # build UI static export into hub/internal/ui/dist
make proto    # regenerate shared contracts (Rust + Go + TS)
make check    # validation across all components (what CI runs)
make dev      # local dev stack (compose: ClickHouse + collector + demo app) [M1+]
```

## Local dev loop

- **UI**: `cd ui && npm run dev` — Next.js dev server with HMR, proxying API
  calls to a locally running hub (see `ui/next.config.ts` rewrites).
- **Hub**: `cd hub && go run ./cmd/hub` — serves the *last built* UI export.
  When iterating on UI, use the Next dev server instead; the embedded copy is
  for production parity.
- **Agent**: `cd agent && cargo check && cargo test` works on macOS (eBPF is
  cfg-gated). Full eBPF build/test requires Linux — use the Docker build image
  (`agent/Dockerfile`) or CI.
- **Full stack**: `make dev` (compose) — from M1.

## Ports (local defaults)

| Service | Port |
|---|---|
| Hub (API + UI) | 8080 |
| Hub OpAMP | 4320 |
| Gateway OTLP gRPC / HTTP | 4317 / 4318 |
| ClickHouse HTTP / native | 8123 / 9000 |
| Next.js dev server | 3000 |

## Docker on macOS (Colima)

The dev stack and integration tests need Docker. With Colima
(`colima start --cpu 4 --memory 8`), testcontainers needs:

```bash
export DOCKER_HOST="unix://$HOME/.colima/default/docker.sock"
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="/var/run/docker.sock"
```

## Common tasks

- **Add an API endpoint**: handler in `hub/internal/api/`, route registration
  in `hub/internal/api/router.go`, storage method on the `storage.Store`
  interface + ClickHouse impl, table-driven test alongside.
- **Change a shared contract**: edit `proto/`, run `make proto`, commit the
  generated code with the change. Never hand-edit generated output.
- **Touch ClickHouse schema**: add a migration in `gateway/schemas/`
  (sequential, never edit an applied migration), update the storage impl and
  its testcontainers integration test.

## Before stopping work

Run the component validation command(s) from `AGENTS.md` for everything you
touched, plus `make proto && git diff --exit-code` if you touched `proto/`.
