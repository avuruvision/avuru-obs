# Building avuru-agent

## On macOS / any OS (logic only)

```bash
cargo check && cargo test && cargo clippy -- -D warnings
```

Everything eBPF is Linux-gated; the above must always stay green off-Linux
(hard rule, see `agent_docs/rust_style.md`).

## Linux / real eBPF build (from M2)

The eBPF programs (`crates/agent-ebpf`, added in M2) build with the aya
toolchain inside the Docker build image:

```bash
docker build -f Dockerfile -t avuru-agent:dev .
```

Kernel requirements at runtime: Linux ≥ 5.8 with BTF. The agent reports a
degraded preflight status instead of crashing on older kernels.
