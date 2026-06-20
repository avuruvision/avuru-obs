# Go Style (hub)

Read when actively writing Go. Baseline: standard Go style (gofmt, Effective
Go) enforced by `golangci-lint` — this file only lists project-specific rules.

## Layout

- `hub/cmd/hub/` — main only: flag/env parsing, wiring, graceful shutdown
- `hub/internal/api/` — HTTP handlers + router; no business logic
- `hub/internal/storage/` — the `Store` interface + per-backend packages
  (`clickhouse/`, fakes in `storagetest/`)
- `hub/internal/opamp/` — OpAMP server
- `hub/internal/auth/` — `Provider` interface + implementations
  (the hub is API-only; the UI is a separate nginx deployable, not embedded)

## Rules

1. **No CGO.** The hub ships as a single static binary
   (`modernc.org/sqlite`, never `mattn/go-sqlite3`).
2. **Storage discipline**: SQL exists only inside
   `internal/storage/clickhouse/`. Handlers depend on the `Store` interface.
3. **Errors**: wrap with `fmt.Errorf("doing x: %w", err)`; sentinel errors in
   the package that owns them (`storage.ErrNotFound`). Handlers map errors to
   HTTP status in one place (the router's error middleware), not per-handler.
4. **Context first**: every Store/external call takes `context.Context` and
   honors cancellation.
5. **stdlib `net/http`** (Go 1.22+ mux) — no web framework. JSON via
   `encoding/json`; validation explicit at the handler boundary.
6. **Logging**: `log/slog` structured logging; never `fmt.Println`. Include
   `trace_id`-style correlation fields where available.
7. **Config**: env vars with `AVURUOPS_` prefix, parsed once in `cmd/hub`,
   passed down as typed structs. No global config reads from packages.
8. **Tests**: table-driven; fakes over mocks; `httptest` for handler tests.
   Keep files under ~300 lines; split by responsibility, not by type.
