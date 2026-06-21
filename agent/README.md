# agent/ — Rust node agent (eBPF flow tracer)

`avuru-agent` is the per-node L4 flow tracer. It watches TCP `connect()`/
`listen()` via eBPF (aya), turns them into flow records, and exports them as
OTLP — this is what makes the **service map light up in minutes with zero
instrumented apps**, independent of traces. It runs as one container in the
[`sensor/`](../sensor/README.md) DaemonSet pod.

See [`agent_docs/architecture.md`](../agent_docs/architecture.md) for where the
agent sits in the pipeline, and [`agent_docs/rust_style.md`](../agent_docs/rust_style.md)
before writing Rust.

## Stack

- **Rust** (edition 2021), Cargo workspace, Apache 2.0.
- [aya](https://aya-rs.dev/) for eBPF; OpenTelemetry / OTLP for export.

## Layout

| Path | What |
|---|---|
| `crates/flow-core/` | Core flow types and logic (platform-independent) |
| `crates/avuru-agent/` | The agent binary: eBPF programs + userspace + OTLP export |
| `rust-toolchain.toml` | Pinned toolchain |
| `Cargo.toml` | Workspace + shared `version` (stamped by `make version-set`) |

## Build & test

```bash
cargo check
cargo test
cargo clippy -- -D warnings    # the lint gate
```

Or from the repo root: `make agent` (build), `make check` (full gate).

## Platform notes

eBPF is `#[cfg]`-gated, so `cargo check`/`cargo test` work on **macOS** for the
userspace and platform-independent code. The **full eBPF build/test requires
Linux** (kernel ≥5.8 + BTF) — use CI or a Linux box. The agent must degrade
gracefully where eBPF is unavailable; never crash-loop on an unsupported kernel.
