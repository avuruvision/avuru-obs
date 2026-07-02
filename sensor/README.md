# sensor/ — DaemonSet assembly

The "sensor" is the per-node DaemonSet pod. It is shipped by the Helm chart
(`deploy/helm/avuruops/templates/sensor-*.yaml`, `sensor.enabled=true` by
default). Composition:

| Container | Source | Role | Ships |
|---|---|---|---|
| `obi` | upstream `otel/ebpf-instrument` (pinned, see `agent_docs/tech_stack.md`) | zero-code traces + RED metrics | v0.1 |
| `otel-agent` | upstream collector image | filelog tailer, kubeletstats | v0.1 |
| `profiler` | upstream OTel eBPF profiler | CPU profiles (OTLP Profiles, alpha) | v0.1 |
| `avuru-agent` | `../agent` (Rust) | L4 flow tracer → service map | **v0.2** |

All containers emit OTLP to the gateway (the profiler emits to the hub's
profiles-ingest seam while the ClickHouse exporter lacks profiles support);
OpAMP-managed config from the Hub lands with the v0.2 OpAMP server.

Kernel preflight: ≥5.8 + BTF required for eBPF containers; an initContainer
warns loudly on unsupported kernels but never blocks — the pod must never
crash-loop because of kernel capability. On such nodes the eBPF containers can
be disabled individually (`sensor.obi.enabled=false`, …) while logs/metrics
collection keeps running.
