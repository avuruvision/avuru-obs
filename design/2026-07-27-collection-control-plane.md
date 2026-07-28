# AEP: Runtime collection control plane — switch collection from the UI

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Make the per-signal / per-namespace / per-pod-label / per-node-label collection
knobs that v0.1 controls through Helm values **switchable from the UI at
runtime**. The hub persists a bounded, schema-validated **collection overlay**,
patches the sensor's ConfigMap(s), and lets the existing config-checksum rollout
restart the DaemonSet — behind a default-off flag and a Kubernetes Role scoped to
the named ConfigMaps only. Settings → Collection stops being read-only. OpAMP
stays the eventual destination (status reporting first; remote config once OBI
grows a client).

## Motivation

Today collection is configured at install time (`deploy/helm/README.md`): you
choose signals, namespaces and labels in values and `helm upgrade`. That is the
right default (declarative, GitOps-friendly) but it makes the common operational
loop — "stop profiling that noisy namespace right now", "turn logs off for this
label for an hour" — a code-change-and-redeploy. Settings → Collection already
shows a **read-only** agent inventory; this closes the loop. It must not dent the
[wedge](../AGENTS.md) (default install unchanged, feature off by default) and must
respect the
[locked decision](../agent_docs/architecture.md#locked-decisions-and-rationale)
that the hub configures agents but is never in the telemetry byte-path.

### Goals

- **UI-authored overlay**: the same knobs (signal families on/off; namespace,
  pod-label, node-label include/exclude) editable in Settings → Collection.
- **Bounded + schema-validated**: the overlay is a small, closed schema (no
  free-form collector YAML from the UI — that would be an injection surface);
  the hub validates and rejects anything outside it.
- **Safe rollout**: the hub writes the sensor ConfigMap; the existing config
  checksum annotation triggers a normal rolling restart. No new agent protocol.
- **Default-off + least privilege**: a `collection.runtimeControl.enabled` flag
  (default false) and a Role granting `get/update` on **only** the named sensor
  ConfigMaps in the release namespace — nothing cluster-wide.
- **Precedence is explicit**: values set the base; the overlay is an additive
  layer the UI can reset; "reset to chart defaults" clears the overlay.

### Non-goals

- **Arbitrary collector-config editing from the UI** — closed schema only.
- **Remote config over OPAMP to OBI** — deferred until OBI ships an OpAMP client
  (a later AEP); v0.2 patches ConfigMaps + rolling restart.
- **Per-agent (per-node) overrides** — overlay is release-wide in v0.2.
- **Query-time filtering** — explicitly rejected (saves no collection/storage).

## Solution

**Overlay store + API.** A `collection_overlay` singleton row (ReplacingMergeTree,
one row per release, the alert-channel pattern) holding the validated overlay
JSON. `GET/PUT /api/v1/collection/overlay` (Admin), schema-validated server-side
against the closed knob set. `GET /api/v1/collection` continues to serve the
inventory + the effective (base ⊕ overlay) config.

**Applier.** A hub component (gated on `runtimeControl.enabled`) that, on overlay
change, renders the sensor ConfigMap(s) from base values ⊕ overlay and writes
them via the Kubernetes API using a namespaced ServiceAccount. The sensor
Deployment/DaemonSet already carries a config-checksum annotation (the
sensor-safe-by-default rollout mechanism); bumping it rolls the pods. The hub
uses `client-go` with a Role/RoleBinding the chart creates:
`get,list,watch,update` on `configMaps` restricted by `resourceNames` to the
sensor ConfigMaps.

```
Settings→Collection ─PUT overlay─▶ hub validate+persist ─▶ applier renders
   ▲  (effective config)                                    sensor ConfigMap
   └───────── inventory + status ◀── OpAMP status ◀── sensor  │ checksum bump
                                                              ▼ rolling restart
```

**OpAMP.** The OCB manifest already reserves `opampextension` "added with the
v0.2 OpAMP server". v0.2 uses OpAMP for **status/inventory reporting** (agents
report effective config + health up to the hub); the *config push* remains the
ConfigMap+checksum path until OBI can consume remote config. This keeps the
control loop observable without inventing an agent protocol.

**Security.** The closed schema is the trust boundary; the applier's RBAC is
namespace- and resourceName-scoped; the flag is default-off so an install opts in
deliberately. Overlay edits are audit-logged (structured logs, per the auth AEP's
audit stance).

### Alternatives considered

- **Free-form collector YAML editor in the UI** — maximally flexible, but a
  remote-code-shaped injection surface into every node's agent; rejected.
- **OpAMP remote-config push now** — the clean end state, but OBI has no OpAMP
  client yet; building a bespoke push channel would be throwaway. Status-only
  now, config-push when upstream lands.
- **Hub mutates the DaemonSet directly (env/args)** — bypasses the ConfigMap and
  the checksum rollout the sensor already trusts; reuse the existing mechanism.
- **Query-time filtering instead of collection control** — already rejected on
  the roadmap: it saves neither collection cost nor storage.

## Verification

- **Unit**: overlay schema validation (accept the closed set, reject unknown
  keys / injection); base ⊕ overlay merge + precedence; ConfigMap render.
- **Integration (envtest/kind)**: the applier writes only the named ConfigMaps
  with the scoped Role; a denied broader write fails closed.
- **e2e (kind)**: toggle logs off for a namespace in the UI → the sensor
  ConfigMap updates → pods roll → logs stop for that namespace, other signals
  unaffected; "reset to defaults" restores the chart config.
- **Wedge/TTV gate**: unchanged with the flag off; a separate gated job exercises
  the on-path.
- **Done** = an admin flips a collection knob in the UI and it takes effect on
  the cluster within a rollout, with no redeploy, and the feature is invisible
  when the flag is off.

## Roadmap

- [ ] AEP accepted
- [ ] `collection_overlay` store + validated `GET/PUT /collection/overlay`
- [ ] Applier (client-go, scoped Role) + ConfigMap render + checksum rollout
- [ ] Settings → Collection becomes writable (effective config + reset)
- [ ] `collection.runtimeControl.enabled` flag + chart Role/RoleBinding (default off)
- [ ] OpAMP status/inventory reporting from agents
- [ ] kind e2e + envtest RBAC test; docs-align (EN/FR)
