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

## What CI proves — and what it does not

The e2e wedge gate (`deploy/helm/e2e-helm.sh`) keeps a **probe-sensitive
canary** — tight CPU limit, aggressive liveness probe, real traffic — Ready
with zero restarts through a soak with the sensor attached. Green means:
attach-time analysis does not stall a tight-probe pod, and steady per-request
instrumentation does not gross-regress it. It does **not** reproduce
production traffic rates against production CPU quotas — which is exactly why
this runbook stages the rollout instead of trusting the gate alone
(AEP [2026-07-17](../../design/2026-07-17-sensor-safe-by-default.md)).
