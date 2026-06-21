# hub/ — Go control plane (API only)

The hub is a single, static Go binary: the **client-agnostic API** plus the
config plane. It serves the REST/WS API, runs the OpAMP server that configures
agents and collectors, owns alerting, and applies the ClickHouse schema
migrations. It reads telemetry from ClickHouse through a `storage.Store`
interface and is **never in the telemetry byte-path**. The UI is a
[separate deployable](../ui/README.md), not embedded.

See [`agent_docs/architecture.md`](../agent_docs/architecture.md) for the data
flow and [`agent_docs/go_style.md`](../agent_docs/go_style.md) before writing Go.

## Stack

- **Go 1.26**, Apache 2.0; distroless container image (`Dockerfile`).
- ClickHouse via `clickhouse-go/v2`; testcontainers-go for integration tests.

## Layout

| Path | What |
|---|---|
| `cmd/hub/` | Entrypoint (also `hub migrate`) |
| `internal/api/` | REST/WS handlers, router, `Version` (set via build ldflags) |
| `internal/storage/` | `storage.Store` interface + ClickHouse impl + `migrations/` (`go:embed`) |
| `internal/auth/` | `auth.Provider` interface (enterprise seam) |
| `Makefile` | `build`, `test`, `test-int`, `lint`, `run` |
| `.air.toml` | Hot-reload config for `air` |

## Build, test, run

```bash
make build          # -> bin/hub  (or: go build ./cmd/hub)
make test           # unit tests (go test -race ./...)
make test-int       # integration: ephemeral ClickHouse via testcontainers
make lint           # golangci-lint
go run ./cmd/hub    # run locally (serves on :8080)
air                 # hot-reload during development
```

From the repo root: `make hub` / `make check`. Integration tests on macOS need
the Colima Docker env vars — see
[`agent_docs/development.md`](../agent_docs/development.md).

## Conventions

- Talk to storage through `storage.Store`; never reach ClickHouse SQL outside
  the ClickHouse implementation package.
- Don't bypass the [enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass):
  auth provider, the `tenant` column, retention policy objects.
- Schema changes are sequential migrations under `internal/storage/migrations/`
  — never edit an applied migration; add a new one.
