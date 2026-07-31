# AEP: Runtime collection control plane — switch collection from the UI

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Accepted (v1 scope below; OpAMP status/inventory reporting split
  out as separate follow-up work)

## Summary

Make the per-signal / per-namespace collection knobs that v0.1 controls
through Helm values **switchable from the UI at runtime**. The hub persists a
bounded, schema-validated **collection overlay**, patches the sensor's
ConfigMap(s), and bumps a hub-owned annotation on the sensor DaemonSet's pod
template to force a rolling restart — behind a default-off flag and a
Kubernetes Role scoped to the named ConfigMaps and the named DaemonSet only.
Settings → Collection stops being read-only for those two knobs. Per-pod-label
and per-node-label opt-out stay exactly as they are today (an operator's own
`kubectl label`, shown as a copy-pasteable command) — see Non-goals. OpAMP
stays the eventual destination for status reporting; remote config push is
still gated on OBI shipping an OpAMP client.

## Motivation

Today collection is configured at install time (`deploy/helm/README.md`): you
choose signals, namespaces and labels in values and `helm upgrade`. That is the
right default (declarative, GitOps-friendly) but it makes the common operational
loop — "stop profiling that noisy namespace right now", "turn logs off for this
label for an hour" — a code-change-and-redeploy. Settings → Collection already
shows a **read-only** agent inventory; this closes the loop for the two knobs
the hub can safely own. It must not dent the [wedge](../AGENTS.md) (default
install unchanged, feature off by default) and must respect the
[locked decision](../agent_docs/architecture.md#locked-decisions-and-rationale)
that the hub configures agents but is never in the telemetry byte-path.

### Goals

- **UI-authored overlay**: whole-signal on/off (OBI traces, logs,
  kubeletstats, profiler, green) and the shared `sensor.collection.
  excludeNamespaces` list, editable in Settings → Collection.
- **Bounded + schema-validated**: the overlay is a small, closed schema (no
  free-form collector YAML from the UI — that would be an injection surface);
  the hub validates and rejects anything outside it.
- **Safe, forced rollout**: the applier patches the sensor ConfigMaps *and*
  patches a hub-owned checksum annotation on the DaemonSet pod template so the
  rollout is guaranteed regardless of whether the collector process itself
  hot-reloads its config file. Idempotent: re-applying identical overlay
  content patches nothing and restarts nothing.
- **Default-off + least privilege**: a `collection.runtimeControl.enabled` flag
  (default false), a dedicated hub-side ServiceAccount, and a namespaced Role
  granting `get/update/patch` on **only** the 4 named sensor ConfigMaps
  (`-sensor-obi`, `-sensor-agent`, `-sensor-profiler`, `-sensor-kepler`) and
  `get/patch` on the named sensor DaemonSet — nothing cluster-wide, nothing on
  any other namespace's resources.
- **Precedence is explicit**: values set the base; the overlay is an additive
  layer the UI can reset; "reset to chart defaults" clears the overlay.

### Non-goals

- **Arbitrary collector-config editing from the UI** — closed schema only.
- **Per-pod-label / per-node-label opt-out from the UI.** Those labels
  (`avuru.obs/instrument`, `avuru.obs/collect`) live on the *target's own* pods
  and nodes, outside the hub's namespace. Giving the hub write access to label
  arbitrary resources cluster-wide would be a materially bigger RBAC surface
  than "named ConfigMaps + one DaemonSet in the release namespace," and cuts
  against the least-privilege goal above. Settings → Collection instead shows
  the exact `kubectl label ...` command from `deploy/helm/README.md` for an
  operator to run themselves.
- **OpAMP status/inventory reporting from agents** — a separate, orthogonal
  read-path capability (agent health visibility). Not required for the overlay
  to actually take effect, so it ships as separate follow-up work rather than
  gating this AEP.
- **Remote config over OpAMP to OBI** — deferred until OBI ships an OpAMP
  client; v1 patches ConfigMaps + a forced rollout.
- **Per-agent (per-node) overrides** — overlay is release-wide in v1.
- **Query-time filtering** — explicitly rejected (saves no collection/storage).

## Solution

**Overlay store.** A `collection_overlay` table (migration `0014`), one
logical singleton row, `ReplacingMergeTree(UpdatedAt)` — same pattern as
`alert_channel` / `project`. The row holds `Overlay String` (a JSON payload
whose shape is owned and validated by a Go struct, not ClickHouse DDL —
5 optional bool fields for the signal toggles, one optional `[]string` for the
namespace-exclude override), `UpdatedAt DateTime`, `UpdatedBy String` for the
audit trail.

**API** (`hub/internal/api/collection.go`, gated behind the existing
`securedAdmin` middleware):

- `GET /api/v1/collection/overlay` — current overlay + the effective (base ⊕
  overlay) config.
- `PUT /api/v1/collection/overlay` — validates against the closed schema,
  upserts with `UpdatedBy` = the authenticated admin, triggers the applier.
- `DELETE /api/v1/collection/overlay` — writes an empty overlay (all fields
  nil), triggers the applier to reconcile back to chart defaults.
- `capabilities` gains `collectionRuntimeControl: bool`, sourced from
  `collection.runtimeControl.enabled` — how the UI decides whether Settings →
  Collection renders writable or (today's) read-only.

**Applier** (new `hub/internal/collection` package; adds `k8s.io/client-go`,
not currently a hub dependency):

- Renders the 4 sensor ConfigMaps in Go, applying only the overridable fields
  (signal enable + `excludeNamespaces`) on top of the chart's base-rendered
  content, and `Update`s each one via `client-go` — skipped if content is
  unchanged.
- Computes a sha256 over the merged content and patches a **new, hub-owned**
  pod-template annotation on the sensor DaemonSet
  (`avuru.obs/overlay-checksum`) — distinct from Helm's own `checksum/config`
  annotation, which stays untouched and keeps working normally for any future
  `helm upgrade`. Patched only if the hash changed, so the rollout is
  idempotent.
- RBAC: a dedicated hub ServiceAccount (the hub currently runs under
  `default`) bound to a namespaced Role — `get,update,patch` on the 4 named
  ConfigMaps, `get,patch` on the named DaemonSet, both restricted by
  `resourceNames`.

```
Settings→Collection ─PUT overlay─▶ hub validate+persist ─▶ applier renders
   ▲  (effective config)                                    sensor ConfigMaps
   └───────────────────── GET overlay ◀─────────────────────┘  │
                                                     hub-owned checksum bump
                                                     on the DaemonSet pod tmpl
                                                              ▼
                                                     kubelet rolling restart
```

**UI** (`ui/src/components/settings/collection-settings.tsx`): when
`collectionRuntimeControl` is true, the existing read-only inventory card
stays, plus a new writable card — 5 toggle switches, an editable namespace
list, and "Reset to chart defaults." When false (today's default), the page
is unchanged from what ships now, including the existing hint text.

**Security.** The closed schema is the trust boundary; the applier's RBAC is
namespace- and resourceName-scoped; the flag is default-off so an install opts
in deliberately. Overlay writes are logged with actor, before/after, and the
resourceVersions touched, via the hub's existing structured logger — no new
audit subsystem.

### Alternatives considered

- **Free-form collector YAML editor in the UI** — maximally flexible, but a
  remote-code-shaped injection surface into every node's agent; rejected.
- **OpAMP remote-config push now** — the clean end state, but OBI has no OpAMP
  client yet; building a bespoke push channel would be throwaway. Deferred
  entirely (not just status-first) to keep this AEP's surface to the
  ConfigMap+rollout path.
- **Rely on live config reload, no DaemonSet RBAC** — patch only the
  ConfigMaps and rely on kubelet's ~1min volume propagation plus the collector
  hot-reloading its config file. Rejected: unverified whether
  `avuru/otelcol-contrib` reloads on file change, and a silent non-reload
  would make toggles look applied (audit-logged, ConfigMap updated) while
  never actually taking effect until the next unrelated pod restart.
- **Plain `kubectl rollout restart`-style annotation instead of a checksum** —
  same RBAC need as the chosen approach, simpler to compute, but not
  idempotent: re-saving identical overlay content would force an unneeded
  restart every time.
- **Hub mutates the DaemonSet env/args directly** — bypasses the ConfigMap
  entirely; rejected in favor of reusing the config-file mechanism the sensor
  already trusts.
- **Query-time filtering instead of collection control** — already rejected on
  the roadmap: it saves neither collection cost nor storage.
- **Overlay also edits pod-label / node-label opt-out** — would require the
  hub to have RBAC to label arbitrary pods/nodes across namespaces it doesn't
  own; rejected as disproportionate to the AEP's own least-privilege goal (see
  Non-goals).

## Verification

- **Unit**: overlay schema validation (accept the closed set, reject unknown
  keys / injection); base ⊕ overlay merge + precedence; ConfigMap render;
  checksum computed identically for identical input (idempotency).
- **Integration (envtest/kind)**: the applier writes only the 4 named
  ConfigMaps and patches only the named DaemonSet, with the scoped Role; a
  denied broader write (another ConfigMap, another namespace) fails closed.
- **e2e (kind)**: toggle logs off for a namespace through the API → the sensor
  ConfigMap updates → the DaemonSet's overlay-checksum annotation changes →
  pods roll → logs stop for that namespace, other signals unaffected; "reset
  to defaults" restores the chart config and rolls again.
- **Wedge/TTV gate**: unchanged with the flag off; a separate gated job
  exercises the on-path.
- **Done** = an admin flips a collection knob in the UI and it takes effect on
  the cluster within one forced rollout, with no `helm upgrade`, and the
  feature (API routes, RBAC objects, UI card) is entirely absent when the flag
  is off.

## Roadmap (v1 — this AEP)

- [x] AEP accepted
- [ ] `collection_overlay` store (migration `0014`) + validated
      `GET/PUT/DELETE /collection/overlay`
- [ ] Applier (`client-go`, scoped Role) + ConfigMap render + hub-owned
      checksum annotation on the DaemonSet
- [ ] `collection.runtimeControl.enabled` flag + chart ServiceAccount/Role/
      RoleBinding (default off) + `capabilities` field
- [ ] Settings → Collection becomes writable for signal toggles + namespace
      excludes (effective config + reset); pod/node opt-out shown as a
      `kubectl` command, not editable
- [ ] Unit + envtest/kind RBAC test + kind e2e; docs-align (EN/FR)

## Follow-up (separate AEP, not blocking this one)

- OpAMP status/inventory reporting from agents up to the hub.
