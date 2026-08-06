# sensor/ — DaemonSet assembly

The "sensor" is the per-node DaemonSet pod. It is shipped by the Helm chart
(`deploy/helm/avuruobs/templates/sensor-*.yaml`, `sensor.enabled=true` by
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

## Kepler degradation story (no RAPL → drop the container, keep collecting)

The energy signal is hardware-dependent: most public-cloud VMs expose no
RAPL/powercap, which is why the container is **born off** — a default-on
energy collector would silently flip on for every install on upgrade.

On a node without RAPL, Kepler does **not** sit there measuring nothing: the
pinned v0.11.4 fails its startup zone discovery (`failed to initialize service
rapl: no RAPL zones found`) and exits, so the container crash-loops and the
sensor pod never reports Ready — which fails `helm --wait` and `kubectl
rollout status` for the whole sensor, an optional signal taking down the
collection that was working. RAPL-less nodes therefore set
**`sensor.green.kepler.enabled=false`**: the container is dropped, and
`sensor.green.estimation.enabled` (TDP model, stamped
`avuruobs_quality="estimated"`) carries `/green` on its own. The preflight
initContainer prints which case a node is in before anything starts. On
partial fleets, run the measured source only where powercap exists.

The container carries **no liveness/readiness/startup probes** so that a
*degraded* Kepler can never flap the sensor pod (the "do no harm" probe-canary
gate in `deploy/helm/e2e-helm.sh` enforces exactly this, with the
fake-cpu-meter producing energy during the soak). Note what that does and does
not buy: absent probes keep a live-but-unhealthy container from being
restarted, but nothing rescues a pod from a container that terminates itself.

The export's coverage ratio measures how much of the *measured* energy was
attributed to workloads, not RAPL reach; node-level coverage from the
collected node counters is a planned follow-up (see the design AEP). CI
environments without RAPL use `sensor.green.fakeCpuMeter=true` (Kepler's dev
fake meter) — never enable it where real hardware exists, it fabricates
measurements.
