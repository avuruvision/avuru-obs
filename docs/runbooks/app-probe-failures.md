# Runbook: application pods fail probes after installing avuru-obs

Symptom: after `helm install avuruops`, application pods in **other**
namespaces go NotReady / CrashLoopBackOff on liveness or readiness probes.
Deleting the avuru-obs namespace makes them healthy again.

Before touching anything, collect evidence:

```bash
tools/diagnose/sensor-impact.sh > sensor-impact.txt
```

The script is read-only. Attach its output to any issue you file.

## Ranked causes and how to tell them apart

### 1. OBI eBPF instrumentation interfering with app processes (most likely)

OBI attaches uprobes to application binaries for zero-code tracing. Two
mechanisms can break tight probes:

- **Uprobe overhead is charged to the app's CPU cgroup.** The int3 trap and
  handler run in the target process context, so a pod with a small CPU limit
  gets throttled and misses probe deadlines. CPU limits on the OBI
  *container* do NOT bound this — they only cap OBI's userspace half.
- **Attach-time analysis delays startup.** ELF/symbol scanning at process
  start can push slow-starting apps past `initialDelaySeconds`.

Evidence that points here:

- Probe failures are **timeouts** (`context deadline exceeded`), not
  `connection refused`, in `kubectl describe pod` events.
- Affected pods have **low CPU limits** and their containers show throttling:
  `nr_throttled`/`throttled_time` climbing in the container's `cpu.stat`.
- OBI logs on the same node mention instrumenting the failing process
  (match PID/exe): `kubectl logs -n <avuru-ns> <sensor-pod> -c obi`.
- Decisive bisection: `helm upgrade ... --set sensor.obi.enabled=false`,
  restart one affected app, watch probes for 5 minutes.

Fixes, least to most drastic:

- Exclude the workload: label the pod template
  `avuru.obs/instrument: "false"` (see `sensor.collection.optOutLabel`).
- Exclude the namespace: add it to `sensor.collection.excludeNamespaces`.
- Exclude the node: `kubectl label node <n> avuru.obs/collect=false`
  (removes the whole sensor pod from that node, no helm upgrade needed).
- Disable tracing cluster-wide: `sensor.obi.enabled=false`.

### 2. Node resource pressure (ClickHouse + sensor starving apps)

The bundled ClickHouse requests 2Gi (limit 4Gi); on small nodes it squeezes
app pods, and a starved **kubelet executes the probes**, so probe timeouts
appear even for healthy apps.

Evidence that points here:

- Failures **concentrate on the node running ClickHouse** (or the busiest
  node) rather than tracking OBI instrumentation.
- `kubectl top nodes` near capacity; `MemoryPressure=True` conditions;
  OOMKilled/eviction events in `kubectl get events -A`.

Fixes: move ClickHouse (`clickhouse.external.enabled`), shrink its
requests/limits, or add nodes. Enable `sensor.priorityClass.create=true` so
the sensor is always the first thing throttled/evicted, never your apps.

### 3. Profiler overhead

First question: was `sensor.profiler.enabled=true`? It defaults to false.
The whole-node eBPF profiler samples every process; its loader also
hard-fails on some kernels. Evidence: profiler container CPU high or
crash-looping; disabling it resolves the symptom.

### 4. kubeletstats load on the kubelet

Least likely at the default 30s interval, but a busy kubelet delays probe
execution for every pod on the node. Evidence: kubelet CPU spikes aligned
with collection ticks. Mitigation: raise
`sensor.agent.kubeletstats.interval` to `60s` or disable the receiver.

## Bisection ladder

Run top to bottom; after each step, `kubectl rollout restart` ONE affected
app and observe probes for ~5 minutes before moving on.

1. `helm upgrade avuruops ... --set sensor.obi.enabled=false`
2. `helm upgrade avuruops ... --set sensor.enabled=false`
3. Reduce/relocate ClickHouse (`values-staging.yaml`-style footprint, or
   `clickhouse.external.enabled=true`)

Wherever the symptom disappears, that layer is your culprit — prefer the
targeted excludes above over leaving a whole subsystem off.

## Why "delete the namespace" seemed to fix it

Deleting the namespace removes the sensor DaemonSet (all eBPF attach), the
gateway, and ClickHouse (and its PVC) in one stroke — all four candidate
causes at once, which is why it "works" but tells you nothing. Use the
bisection ladder instead so the fix can be permanent and targeted.
