# Avuru Obs

**All-in-one observability — traces, metrics, logs, continuous profiling.
Live in 5 minutes, zero code changes.**

Avuru Obs replaces the Grafana LGTM stack with one storage engine
(ClickHouse), one binary control plane, and one UI. eBPF auto-discovers your
services: install the Helm chart and watch the service map light up — no SDK,
no sidecars, no YAML archaeology.

> Status: pre-v0.1, under active development. The design spec lives in
> [docs/superpowers/specs/2026-06-12-avuru-obs-design.md](docs/superpowers/specs/2026-06-12-avuru-obs-design.md).

## How it works

```
sensor DaemonSet (eBPF: flows · traces · RED · logs · profiles)
        │ OTLP
        ▼
gateway (minimal OTel Collector) ──► ClickHouse (all signals)
                                          ▲ SQL
hub (single Go binary: API + UI + OpAMP config plane)
```

- **Zero-code**: OpenTelemetry eBPF Instrumentation (OBI) for traces + RED
  metrics; our Rust flow tracer for the live service map; OTLP ingest for
  apps you've already instrumented.
- **One store**: ClickHouse for traces, metrics, logs, profiles, and flows.
- **One artifact**: the hub serves API, UI, alerting, and agent config
  (OpAMP) from a single static binary.

## Repository layout

| Path | What |
|---|---|
| `agent/` | Rust node agent (aya eBPF flow tracer) |
| `hub/` | Go control plane + embedded UI |
| `gateway/` | Minimal OTel Collector distro (OCB) + ClickHouse schemas |
| `ui/` | Next.js static-export SPA |
| `sensor/` | DaemonSet assembly (agent + OBI + collector + profiler) |
| `proto/` | Shared contracts (codegen to Rust/Go/TS) |
| `deploy/` | Helm chart (flagship) + docker-compose sandbox |

## Developing

Start with [AGENTS.md](AGENTS.md) (yes, even humans) and
[agent_docs/development.md](agent_docs/development.md).

```bash
make ui hub   # build the UI export and the hub binary embedding it
make check    # everything CI runs
```

## License

Apache 2.0
