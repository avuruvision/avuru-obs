# AEP: Make the eBPF sensor safe to leave on

- **Date:** 2026-07-17
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

The zero-code service map is the [wedge](../AGENTS.md), and it comes from the
sensor DaemonSet (OBI, eBPF). Yet in the real LAN deployment the sensor is
turned **off** (`sensor.enabled: false` in `avuru-ops`'s `values-lan.yaml`)
because OBI's uprobe overhead pushed some applications past their liveness-probe
deadlines. The headline promise ships disabled. This AEP keeps the sensor **on
by default** but makes that safe: a documented staged-rollout path, a new
per-workload discovery *mode* for cautious fleets, and — the load-bearing part —
a time-to-value gate that fails if a probe-sensitive workload regresses while
the sensor is attached. "Safe" stops being a claim and becomes something CI
proves.

## Motivation

The wedge is law: a fresh cluster reaches a live service map in under five
minutes, zero app changes. Today that is enforced in CI on a demo that is *not*
probe-sensitive, so a green gate does not actually mean the sensor is safe to
run next to real workloads — and the one place we run it next to real workloads,
the sensor is off. The promise is unproven where it matters and disabled where
it is felt.

The root cause is understood (see
[`docs/runbooks/app-probe-failures.md`](../docs/runbooks/app-probe-failures.md)):

- OBI attaches uprobes to application binaries. The int3 trap and its handler
  run **in the target process's context**, so the cost is charged to the
  **application's** CPU cgroup, not OBI's. A pod with a tight CPU limit gets
  throttled and misses probe deadlines.
- A CPU limit on the OBI *container* does not bound this — it caps only OBI's
  userspace half. `sensor.priorityClass` does not help either — it governs the
  sensor's own scheduling, not the app cgroup being throttled.

So the two knobs an operator reaches for first are the two that cannot fix it,
which is exactly how you end up disabling the whole sensor. The failure mode is
narrow (tight-CPU-limit workloads with aggressive probes), but when it bites it
bites the customer's app, not ours — the worst place to learn about it.

### Goals

- The sensor stays **on by default**; the zero-code promise is kept, not walked
  back to opt-in.
- CI proves a probe-sensitive workload survives with the sensor attached, so a
  green wedge gate is a real safety signal.
- Give cautious operators a bounded, documented way to narrow instrumentation
  without turning the sensor off wholesale.
- Re-enable the sensor in the LAN deployment once the gate is green.

### Non-goals

- Removing uprobe overhead. The durable fix is the lower-overhead kernel path
  (the v0.2 flow work), tracked separately; this AEP makes the *current* path
  safe to run.
- Fixing kernel-specific OBI/loader failures (a different failure class, already
  handled by the preflight and per-container switches).
- Any change to what is collected or stored — this is about *how safely* it is
  collected, and touches no schema or enterprise seam.

## Solution

Layered, cheapest first. The escape hatches already exist in the chart; the new
pieces are the discovery *mode* and the gate.

1. **Keep instrument-all (opt-out) as the default, add an opt-in mode.** A new
   `sensor.obi.discovery.mode: optOut | optIn` (default `optOut` — today's
   behaviour). In `optIn`, OBI instruments only workloads that carry
   `avuru.obs/instrument: "true"`, so a probe-sensitive fleet can adopt tracing
   deliberately, namespace by namespace, without giving up logs, metrics or the
   node inventory. This is a values + `values.schema.json` change plus the
   corresponding OBI discovery config; it does not alter the default install.

2. **Conservative defaults + the existing escape hatches.** The chart already
   ships `sensor.collection.excludeNamespaces` (control-plane namespaces, and
   the release's own), the `avuru.obs/instrument=false` pod opt-out, and the
   `avuru.obs/collect=false` node opt-out. Document them as the first response
   to a probe regression (targeted excludes) rather than disabling the sensor,
   and recommend CPU headroom on instrumented apps with tight limits.

3. **Staged rollout runbook.** Pin the sensor to a canary node pool first
   (`sensor.nodeSelector`), soak, watch app probes, then widen. A DaemonSet
   already rolls one node at a time by default (`maxUnavailable: 1`), so a bad
   pin surfaces on one node, not the fleet.

4. **`priorityClass` stays on in production.** It does not fix the cgroup issue,
   but it ensures the sensor loses every scheduling and eviction contest, so it
   is never the thing that starves an app. Already set in `values-prod.yaml`.

5. **Make "safe" measurable — the load-bearing change.** Extend the wedge demo
   (`deploy/demo/wedge/wedge.yaml`) with a pod that reproduces the actual
   failure mode: a tight CPU limit and an aggressive `livenessProbe`. Strengthen
   the existing regression gate in `deploy/helm/e2e-helm.sh` so that, after the
   soak with the sensor attached, that pod must still be Ready with zero
   restarts — in addition to the service map appearing under the < 300 s bound.
   Green means the sensor is safe to enable; a regression fails the release.

Only once that gate is green do we flip `sensor.enabled: true` in the LAN
`values-lan.yaml` (and set it explicitly in `values-prod.yaml`).

This touches no schema, no Hub API, and no
[enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass) —
it is a chart-surface + CI change plus one new values field.

### Alternatives considered

- **Switch the global default to opt-in.** Safest, but it abandons the
  zero-code wedge: nothing lights up until someone labels workloads. Rejected as
  a default; offered as `discovery.mode: optIn` for those who want it.
- **Leave the sensor off by default.** This is the status quo in LAN, and it is
  the promise not being delivered. Rejected.
- **Cap OBI harder / raise the app's `initialDelaySeconds`.** The first does not
  work (the cost is in the app cgroup); the second asks every user to retune
  probes cluster-wide for our benefit. Both rejected as primary answers, though
  the runbook still lists probe tuning as a local mitigation.

## Verification

- `deploy/helm/e2e-helm.sh` gains a probe-sensitive canary and asserts it stays
  Ready / zero-restarts through the soak with the sensor attached, alongside the
  existing < 300 s service-map assertion. This is the definition of done: the
  gate is the safety proof.
- `render` assertions in `deploy/helm/template-test.sh` cover
  `sensor.obi.discovery.mode=optIn` (only labeled workloads are instrumented;
  logs/metrics/inventory unaffected) and its default `optOut`.
- `tools/diagnose/sensor-impact.sh` remains the manual, read-only evidence tool
  referenced by the runbook for field diagnosis.
- Acceptance to flip LAN: the hardened gate green on `main`, then
  `sensor.enabled: true` in `values-lan.yaml`, watched for a soak in the real
  cluster.

## Roadmap

- [ ] AEP accepted
- [ ] `sensor.obi.discovery.mode` (values + schema + OBI discovery config)
- [ ] Probe-sensitive workload added to `deploy/demo/wedge/wedge.yaml`
- [ ] `e2e-helm.sh` regression gate asserts it survives the sensor
- [ ] `template-test.sh` covers `optIn` / `optOut`
- [ ] Staged-rollout runbook (`docs/runbooks/sensor-rollout.md`)
- [ ] Flip `sensor.enabled: true` in LAN once the gate is green
