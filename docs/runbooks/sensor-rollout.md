# Runbook: staged sensor rollout (enabling eBPF on a running fleet)

When to use this: you are enabling the sensor (`sensor.enabled: true`) on a
cluster with real workloads, or re-enabling it after a probe incident
([app-probe-failures](app-probe-failures.md)). The failure mode this guards
against is narrow but lands on the customer's app: uprobe overhead is charged
to the **application's** CPU cgroup, so a pod with a tight CPU limit and an
aggressive liveness probe can miss deadlines. CPU limits on the OBI container
and `sensor.priorityClass` cannot bound it — do not reach for those first.

## Know your escape hatches before you start

All pre-existing, cheapest first. A probe regression gets a **targeted**
response from this table — not `sensor.enabled=false`. (The authoritative
knob-by-signal matrix is in `deploy/helm/README.md`, "Deactivating
collection".)

| Symptom / scope | Action | Rolls pods? |
|---|---|---|
| One app misses probes | label its pod template `avuru.obs/instrument: "false"` | app only |
| A namespace of tight-limit apps | add it to `sensor.collection.excludeNamespaces` | sensor only |
| A whole node misbehaves | `kubectl label node <n> avuru.obs/collect=false` | none — instant, no helm |
| Widespread probe timeouts | pause widening; `sensor.obi.discovery.mode=optIn` — uprobes ONLY on pods labeled `avuru.obs/instrument: "true"`, logs/metrics/inventory keep flowing | sensor only |
| Suspected non-OBI cause | bisection ladder in [app-probe-failures](app-probe-failures.md) | — |
| Last resort | `sensor.obi.enabled=false`, then `sensor.enabled=false` | sensor |

## Stage 0 — preconditions

- `sensor.priorityClass.create=true` (set in `values-prod.yaml`). It does not
  fix the cgroup issue, but the sensor then loses every scheduling and
  eviction contest — it is never the thing that starves an app.
- Inventory the blast radius: workloads with **tight CPU limits + aggressive
  probes** (small `timeoutSeconds`/`failureThreshold`, low `cpu` limits) are
  the ones that break. Give them CPU headroom, pre-label them
  `avuru.obs/instrument: "false"`, or adopt `optIn` mode and label the rest.

## Stage 1 — canary node pool

Pin the sensor to a canary pool first:

```bash
kubectl label node <canary-node> avuru.obs/rollout=canary
helm upgrade avuruops ... --reuse-values \
  --set-json 'sensor.nodeSelector={"avuru.obs/rollout":"canary"}'
```

A DaemonSet rolls one node at a time by default (`maxUnavailable: 1`), so even
a bad widening bites one node before the fleet.

## Stage 2 — soak and watch

Soak for at least one business-peak cycle. Watch, on canary nodes:

```bash
# Probe failures fleet-wide (the symptom that matters)
kubectl get events -A --field-selector reason=Unhealthy --watch
# Restart counts on tight-limit workloads
kubectl get pods -A -o wide | awk '$5+0 > 0'
# CPU throttling on a suspect pod (cause #1 evidence)
kubectl exec -n <ns> <pod> -- cat /sys/fs/cgroup/cpu.stat   # nr_throttled climbing?
```

Collect read-only evidence with `tools/diagnose/sensor-impact.sh` — attach it
to any issue. If a workload regresses: escape-hatch it (table above), keep the
rollout moving.

## Stage 3 — widen

Label the next pool and repeat Stage 2; when the last pool is green, remove
`sensor.nodeSelector` so new nodes are covered by default. Then delete the
rollout labels.

On any regression while widening, respond from the escape-hatch table above —
targeted first, never `sensor.enabled=false` as the opening move.

## Adding the green (Kepler) container to a running fleet

Enabling energy collection (`modules.green.enabled` + `sensor.green.enabled`)
rolls the sensor DaemonSet — a fourth container joins the pod — so it goes
through the SAME ladder: canary pool → soak → widen. Green-specific points:

- **Check RAPL homogeneity first.** The canary pool must be representative of
  the fleet's hardware: on each node class, check
  `ls /sys/class/powercap/intel-rapl*` (empty or absent = no RAPL = no energy
  data from that node). A canary pool of RAPL-less nodes proves only that
  Kepler does no harm — it says nothing about the data path. Mixed fleets are
  fine and expected: `/green`'s coverage ratio makes the RAPL-less share
  visible; it is not a rollout failure.
- **Kepler has no probes by design**, so it can never flap the sensor pod —
  during the soak, watch the *sensor* pods stay Ready and the kepler container
  for crash loops (`kubectl logs ds/<release>-sensor -c kepler`); a
  crash-looping kepler is a config/hardware problem to fix or escape-hatch,
  not a fleet risk.
- **Verify before prod trust**: metric names, config keys, port and RBAC
  against the pinned Kepler are a documented verify-on-RAPL-hardware item
  (design/2026-07-22-green-carbon.md) — treat the first canary soak on real
  RAPL nodes as that verification, and confirm `kepler_*` rows land (the
  `/green` dashboard lights up) before widening.
- **Escape hatch**: `sensor.green.enabled=false` rolls only the sensor and
  removes the container; the hub side (`modules.green`) can stay on — the
  dashboard degrades to its empty state, nothing errors.

## What CI proves — and what it does not

The e2e wedge gate (`deploy/helm/e2e-helm.sh`) keeps a **probe-sensitive
canary** — tight CPU limit, aggressive liveness probe, real traffic — Ready
with zero restarts through a soak with the sensor attached. Green means:
attach-time analysis does not stall a tight-probe pod, and steady per-request
instrumentation does not gross-regress it. It does **not** reproduce
production traffic rates against production CPU quotas — which is exactly why
this runbook stages the rollout instead of trusting the gate alone
(AEP [2026-07-17](../../design/2026-07-17-sensor-safe-by-default.md)).
