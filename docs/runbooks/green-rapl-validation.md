# Runbook: validating green energy on real RAPL hardware

When to use this: before you trust the Green screen's numbers in production.
Everything the green module ships has been exercised against Kepler's **dev
fake-cpu-meter** (kind has no powercap, so CI cannot do better) and against
seeded rows. That proves the plumbing, not the physics. Two AEP items are
still open on this and this runbook is how they close:

- [green-carbon](../../design/2026-07-22-green-carbon.md) — *confirm Kepler
  config keys, metric names/labels, port and RBAC on real RAPL hardware*
  (**blocks prod use**).
- [green TDP estimation](../../design/2026-07-28-green-tdp-estimation.md) —
  *side-by-side on one RAPL machine to publish the observed error band*, so
  the docs cite a measured figure instead of a literature one.

You need one node with working Intel/AMD RAPL — a bare-metal or metal-instance
node, not a normal VM. Everything below runs on that one node.

## Stage 0 — is this node actually RAPL-capable?

```bash
kubectl debug node/<node> -it --image=busybox -- ls /sys/class/powercap/
# want: intel-rapl:0 (and usually intel-rapl:0:0, intel-rapl-mmio, ...)
```

An empty listing means no RAPL. Stop here and pick another node — Kepler
v0.11.4 does not idle on such a node, it **exits** with "no RAPL zones found",
which crash-loops the container. That is the node the TDP estimator exists
for, not the node this runbook wants.

Record the CPU model too (`lscpu | grep 'Model name'`): the error band you
publish in stage 5 is only meaningful next to the silicon it was measured on.

## Stage 1 — enable green on that node only

Keep the blast radius at one node — pin the sensor with a node selector for
the duration, exactly as in [sensor-rollout](sensor-rollout.md) stage 1.

```bash
kubectl label node <node> avuru.obs/green-validation=true
helm upgrade avuruobs ... --reuse-values \
  --set-json 'sensor.nodeSelector={"avuru.obs/green-validation":"true"}' \
  --set modules.green.enabled=true \
  --set modules.infraMetrics.enabled=true \
  --set sensor.green.enabled=true \
  --set sensor.green.kepler.enabled=true \
  --set sensor.green.fakeCpuMeter=false
```

`fakeCpuMeter=false` is the whole point: this is the first run where the
numbers come from the hardware.

## Stage 2 — confirm Kepler boots and reads real zones

```bash
kubectl logs -n avuruobs ds/avuruobs-sensor -c kepler --tail=50
```

Check, and write down what you see:

| What to confirm | Why it matters |
|---|---|
| No "no RAPL zones found" / no restarts | the crash-loop case above |
| The zones it reports (package, core, dram, psys?) | which zones the energy actually covers |
| It is serving on **28282** | the chart pins `sensor.green.port: 28282`; a different upstream default silently yields no scrape |
| No permission errors reading powercap | see stage 3 |

Restart count is the fast check: `kubectl get pod -n avuruobs -l
app.kubernetes.io/component=sensor -o wide` — a climbing count on the kepler
container is stage 0 or stage 3 telling you something.

## Stage 3 — the privilege question

The chart defaults to `sensor.green.privileged: true` because that is the
reliable way to read powercap across runtimes and kernels. The
least-privilege path exists but is explicitly marked verify-on-hardware:

```bash
helm upgrade avuruobs ... --reuse-values --set sensor.green.privileged=false
kubectl logs -n avuruobs ds/avuruobs-sensor -c kepler --tail=50
```

If Kepler still reads energy with only `DAC_READ_SEARCH` / `SYS_PTRACE` /
`PERFMON`, say so in the write-up — that is a real hardening result, and it
is the only way anyone can rely on it. If it fails, revert to `true` and
record the exact error; the values comment should then stop offering the
option as if it were merely untested.

## Stage 4 — measured rows, end to end

Scrape → gateway → ClickHouse → API → UI. Check each hop rather than only the
last, so a break has an address.

```bash
# 1. Kepler's own endpoint, from inside the sensor pod (it is loopback-bound).
kubectl exec -n avuruobs ds/avuruobs-sensor -c otel-agent -- \
  wget -qO- http://127.0.0.1:28282/metrics | grep -E '^kepler_(node|pod)_cpu_(watts|joules_total)'
```

The four names must match `sensor.green.metrics.keep`
(`kepler_(node|pod)_cpu_(watts|joules_total)`) and the pod series must carry
`pod_name` / `pod_namespace` labels — those exact strings are what
`green.metrics.podNameAttr` / `podNamespaceAttr` bind in SQL. **A rename
upstream is the single most likely way this breaks**, and it breaks silently:
the keep-regex drops the series and the Green screen simply shows nothing.

```bash
# 2. Rows landing, tagged measured (not estimated).
kubectl -n avuruobs exec avuruobs-clickhouse-0 -- clickhouse-client -u avuru --password <pw> -q "
SELECT MetricName, Attributes['avuruobs_quality'] AS quality, count()
FROM otel.otel_metrics_sum
WHERE MetricName LIKE 'kepler%' AND TimeUnix > now() - INTERVAL 15 MINUTE
GROUP BY MetricName, quality ORDER BY MetricName"
```

Want: `kepler_node_cpu_joules_total` and `kepler_pod_cpu_joules_total`, all
with `quality = measured`. Any `estimated` row here means the TDP estimator
also activated — its RAPL probe should have found powercap and stayed
dormant, so that is a finding worth reporting.

```bash
# 3. The API, then the screen.
curl -s -H "Authorization: Bearer $TOKEN" \
  "$HUB/api/v1/green/summary?start=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)&end=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | jq '.nodes, .totals' 
```

The per-node table (`nodes[]`) must list the node with a non-zero `wh` and
`quality: "measured"`, and coverage must count it under measured — not
absent. Then open `/green` in the UI and confirm the same node reads the same
way.

Sanity-check the magnitude before believing any of it: an idle server package
draws on the order of tens of watts, a loaded one low hundreds. A per-node
figure three orders of magnitude off is a unit error (J vs Wh vs kWh), not a
discovery.

## Stage 5 — the side-by-side error band

This is what turns the estimator's documented accuracy from a citation into a
measurement: model the same node, over the same workload, at the same time as
Kepler measures it.

Do **not** try to force the DaemonSet's estimator to run here. It probes
`/sys/class/powercap/intel-rapl*` once at startup and, finding RAPL, serves an
empty `/metrics` for the process lifetime — by design, so a node can never
report measured and estimated energy at once. Forcing it would also stamp
`avuruobs_quality="estimated"` rows into the tenant's real data.

Instead run a **throwaway estimator pod** on the same node with `/proc`
mounted and `/sys` deliberately absent. Node power comes from `/proc/stat`
utilization alone, so the model runs fully; the missing `/sys` is exactly what
makes the RAPL probe report "no RAPL" and switch it on. Nothing scrapes this
pod, so nothing reaches ClickHouse — you read it directly.

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: tdp-sidebyside
  namespace: avuruobs
spec:
  nodeName: <node>              # the RAPL node from stage 0
  restartPolicy: Never
  containers:
    - name: tdp-estimator
      image: ghcr.io/avuruvision/avuru-obs-tdp-estimator:<appVersion>
      args: ["--listen=:28283", "--node-name=$(NODE_NAME)"]
      env:
        - name: NODE_NAME
          valueFrom: { fieldRef: { fieldPath: spec.nodeName } }
      volumeMounts:
        - { name: host-proc, mountPath: /proc, readOnly: true }
  volumes:
    - name: host-proc
      hostPath: { path: /proc }
EOF

kubectl -n avuruobs logs tdp-sidebyside | head
# want: "no RAPL detected, estimating via TDP model"
#  plus: "resolved power coefficients" — note tier and provenance
```

The provenance line matters as much as the numbers: with no node annotations
and no `--idle-watts/--max-watts`, the coefficients come from the bundled
table by CPU model, or from the **generic fallback** if the model is not in
it. An error band measured on the fallback says something quite different
from one measured on a matched entry — record which you got. Pass
`--idle-watts` / `--max-watts` explicitly if you want to characterise a
specific tier instead.

Then put the node under a few *steady* load levels — idle, ~50%,
near-saturation — holding each for at least 5 minutes, and sample both
sources at the start and end of each level:

```bash
# Estimated (throwaway pod, cumulative joules)
kubectl -n avuruobs exec tdp-sidebyside -- \
  wget -qO- http://127.0.0.1:28283/metrics | grep kepler_node_cpu_joules_total

# Measured (Kepler, same node, same counter name)
kubectl -n avuruobs exec ds/avuruobs-sensor -c otel-agent -- \
  wget -qO- http://127.0.0.1:28282/metrics | grep kepler_node_cpu_joules_total
```

Both are cumulative counters, so the energy for a level is the difference
between its end and start readings. Per level, report measured Wh, estimated
Wh, and the signed relative error.

Publish the **range across levels**, not a single figure: the estimator's
weakness is the shape of the power curve, not a constant offset, so one
number at one load level would overstate what was learned. Note alongside it
the CPU model, the coefficient tier and provenance, and the RAPL zones from
stage 2 — Kepler's "measured" number covers the zones it found, so a
package-only node and a package+DRAM node are not the same comparison.

```bash
kubectl -n avuruobs delete pod tdp-sidebyside
```

## Stage 6 — write it down, then close the boxes

The run only counts if someone else can act on it. Record:

- Node CPU model, kernel, container runtime, and the RAPL zones found.
- Kepler version actually run (`v0.11.4` unless you overrode it), the metric
  names and labels as observed, the port, and whether `privileged=false`
  sufficed.
- Any config key, metric name or label that differed from the AEP table —
  with the values override that fixed it.
- The observed estimator error band and the conditions it was measured under.

Then, and only then:

- Tick *"Confirm Kepler config keys, metric names/labels, port and RBAC on
  real RAPL hardware"* in the green-carbon AEP — it is the item that blocks
  prod use.
- Tick the RAPL side-by-side item in the TDP-estimation AEP and replace the
  literature accuracy figure in the docs with the observed band.
- Run the docs-align pass so the public docs carry the measured numbers
  (EN + FR).

If anything differed from the AEP table, the defaults in
`deploy/helm/avuruobs/values.yaml` (`sensor.green.metrics.keep`,
`green.metrics.*`, `sensor.green.port`) are the things to change — they exist
as values precisely so this run can correct them without a rebuild.

## Rolling back

Green is opt-in and additive; nothing here touches the trace/log/metric path.

```bash
helm upgrade avuruobs ... --reuse-values \
  --set sensor.green.enabled=false --set sensor.green.estimation.enabled=false
kubectl label node <node> avuru.obs/green-validation-
```

`modules.green.enabled=false` additionally hides the Green screen and stops
budget evaluation. The kepler container carries no probes by design, so even
a crash-looping one never flaps the sensor pod — but it also never reports
Ready, which is why `helm --wait` on a RAPL-less node is the symptom that
sends people here in the first place.
