# Shared Contracts (`proto/`)

`proto/` is the **single source of truth** for every structure that crosses a
language boundary (Rust agent ↔ Go hub/gateway ↔ TS UI).

## What lives here

- Flow records emitted by `avuru-agent` (beyond standard OTLP)
- Hub REST/WS API types consumed by the UI (OpenAPI spec)
- OpAMP custom-message payloads (agent config, preflight reports)

What does NOT live here: anything already covered by OTLP/OTel semantic
conventions — use the standard, don't wrap it.

## Rules

1. **Never hand-edit generated code.** Generated output lives in
   `*/generated/` (Go: `hub/internal/generated/`, Rust:
   `agent/crates/proto/src/generated/`, TS: `ui/src/generated/`) and carries a
   `Code generated — DO NOT EDIT` header.
2. **One PR = contract + codegen + both sides.** Run `make proto` and commit
   the regenerated code together with the `.proto`/OpenAPI change and the
   consuming code. CI enforces `make proto && git diff --exit-code`.
3. **Backward compatibility**: never renumber or reuse protobuf field
   numbers; additive changes only once v0.1 ships. Breaking changes need a
   versioned message/endpoint.
4. **OTel first**: field names follow OTel semantic conventions where one
   exists (`service.name`, `k8s.namespace.name`, ...).
