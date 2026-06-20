# Rust Style (agent)

Read when actively writing Rust. Baseline: `rustfmt` defaults +
`cargo clippy -- -D warnings`. This file lists project-specific rules.

## Layout (cargo workspace in `agent/`)

- `crates/flow-core/` — pure logic: flow aggregation, rollups, OTLP mapping.
  **No eBPF, no tokio, fully testable on any OS.**
- `crates/agent-ebpf/` — the eBPF programs (aya-ebpf). Linux-only.
- `crates/avuru-agent/` — the binary: loads eBPF, drives flow-core, exports
  OTLP (opentelemetry-rust), exposes health endpoint.

## Rules

1. **Keep `cargo check` green on macOS.** Everything touching aya/eBPF is
   behind `#[cfg(target_os = "linux")]` and target-specific dependencies in
   `Cargo.toml`. Real builds run in the Docker build image / CI.
2. **Logic lives in `flow-core`.** The eBPF and binary crates stay thin; if
   you can unit-test it, it belongs in flow-core.
3. **No `unwrap()`/`expect()` outside tests and startup.** Use `thiserror`
   for typed errors in libraries, `anyhow` only in the binary crate.
4. **Footprint is a feature**: this runs on every node. Justify new
   dependencies; prefer std; watch allocation in the per-event path (the eBPF
   ring-buffer consumer must not allocate per event).
5. **Async**: tokio only in `avuru-agent`; flow-core is sync.
6. **OTel mapping**: flow records map to OTel semantic conventions
   (`service.name`, `k8s.*`); the wire contract is defined in `proto/`, never
   ad-hoc structs.
7. **Unsafe**: eBPF interop only, each block commented with the invariant
   that makes it sound.
