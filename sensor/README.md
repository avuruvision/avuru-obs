# sensor/ — DaemonSet assembly

The "sensor" is the per-node DaemonSet pod wiring together (M2–M4):

| Container | Source | Role |
|---|---|---|
| `avuru-agent` | `../agent` (Rust) | L4 flow tracer → service map |
| `obi` | upstream image, version-pinned | zero-code traces + RED metrics |
| `otel-agent` | upstream collector image | filelog tailer, kubeletstats |
| `profiler` | upstream OTel eBPF profiler | CPU profiles (OTLP Profiles, alpha) |

All containers emit OTLP to the gateway and receive config from the Hub over
OpAMP (M5). Kernel preflight: ≥5.8 + BTF required for eBPF containers; the
pod degrades to logs/metrics-only on older kernels — it must never crash-loop
because of kernel capability.
