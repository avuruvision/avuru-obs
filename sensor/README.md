# sensor/ — DaemonSet assembly

The "sensor" is the per-node DaemonSet pod. It is shipped by the Helm chart
(`deploy/helm/avuruops/templates/sensor-*.yaml`, `sensor.enabled=true` by
default). Composition:

| Container | Source | Role | Ships |
|---|---|---|---|
| `obi` | upstream `otel/ebpf-instrument` (pinned, see `agent_docs/tech_stack.md`) | zero-code traces + RED metrics | v0.1 |
| `otel-agent` | upstream collector image | filelog tailer, kubeletstats | v0.1 |
| `profiler` | upstream OTel eBPF profiler | CPU profiles (OTLP Profiles, alpha) | v0.1 |
| `kepler` | upstream CNCF Kepler (pinned, see `agent_docs/tech_stack.md`) | per-pod/node CPU energy from RAPL, feeds the green module | **opt-in** (`sensor.green.enabled` + `modules.green.enabled`) |

All containers emit OTLP to the gateway (the profiler emits to the hub's
profiles-ingest seam while the ClickHouse exporter lacks profiles support);
OpAMP-managed config from the Hub lands with the v0.2 OpAMP server. Kepler is
the exception in shape only: it exposes a Prometheus endpoint bound to
loopback, and a `prometheus` receiver in the same pod's otel-agent scrapes it
onto that same OTLP path — nothing else may reach the port.

Kernel preflight: ≥5.8 + BTF required for eBPF containers; an initContainer
warns loudly on unsupported kernels but never blocks — the pod must never
crash-loop because of kernel capability. On such nodes the eBPF containers can
be disabled individually (`sensor.obi.enabled=false`, …) while logs/metrics
collection keeps running.

## Kepler degradation story (no RAPL → no data, no harm)

The energy signal is hardware-dependent: most public-cloud VMs expose no
RAPL/powercap, which is why the container is **born off** — a default-on
energy collector would silently flip on for every install on upgrade. On a
node without RAPL, Kepler simply measures nothing: v1 reports only what was
measured (no TDP estimation), `/green` shows a teaching empty state, and the
preflight initContainer prints a loud warning. Partial fleets (some nodes with
RAPL, some without) surface as a coverage ratio in every export rather than as
an error. To keep a RAPL-less Kepler from ever destabilizing collection, the
container carries **no liveness/readiness/startup probes** — it may sit idle
or unhealthy, but it can never flap the sensor pod (the "do no harm"
probe-canary gate in `deploy/helm/e2e-helm.sh` enforces exactly this, with the
fake-cpu-meter producing energy during the soak). CI environments without RAPL
use `sensor.green.fakeCpuMeter=true` (Kepler's dev fake meter) — never enable
it where real hardware exists, it fabricates measurements.
