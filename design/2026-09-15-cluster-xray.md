# Cluster X-Ray — observed Pod placement and connections

Status: Direction approved by the maintainer from the private HTML prototype;
implementation tracked in #237.

## Why

The node inventory reports resource usage but does not show the relationship
between infrastructure, scheduling and running workloads. An optional isometric
view makes that placement explorable without requiring new collection setup.

## Decision

- Add `view=xray` to Infrastructure alongside the existing default inventory.
  Lazy-load Three.js (MIT, pinned to the prototype's 0.180.0) only for this view.
  Keep static export, project authorization, module gating and global time.
- Read existing node/Pod metrics. Show absolute CPU/memory, not fabricated
  capacities, Kubernetes readiness, hardware identities or cluster names.
  Chassis are labeled as logical infrastructure, not discovered physical hosts.
- Add `/api/v1/infra/pod-connections`, behind `storage.Store`, to read bounded
  connections from Client spans. Match Server children by tenant, trace and
  parent span identity. Resolve Pods only from resource attributes; a recorded
  destination address without a matched Pod remains an unresolved peer, shown
  with a globe. That is not proof that the peer is on the public Internet.
- Exclude auxiliary calls. Count and latency come from caller spans. No flow
  from workload-name guesses or synthetic traffic. Empty or failed connection
  reads do not hide the placement scene. No schema or collector change.
- Render bounded nodes, Pods and particles with visible limits. Keep the full
  returned inventory available and offer filters to narrow the scene. Preserve
  selection and namespace/node/name filters in the URL. Never select another
  Pod silently when the selected identity disappears.
- Include orbit, zoom, reset, layer visibility, transparency, exploded spacing,
  Pod selection and isolation, plus a keyboard-accessible selection control.
  Suspend animation for reduced motion, background tabs and offscreen scenes;
  release GPU resources on unmount. Provide an inventory fallback without WebGL.
- The scene uses its own semantic forest surface in both application themes.
  Error color describes observed request errors only, not Pod readiness.

## Verification and delivery

API tests cover validation, project authorization, module gating, errors and
truncation. ClickHouse tests cover exact Pod attribution, namespace separation,
tenant isolation, time bounds, unmatched destinations and request aggregation.
Browser tests cover real seeded placement, fixture-backed connections, controls,
selection URLs, missing data, reduced motion, mobile and WebGL failure.

Run make check, hub lint, storage integration coverage, authenticated make e2e-ui
and chart checks. Merge the feature PR, finalize v0.18.0 in a release PR, push a
signed tag and the release branch, verify published artifacts, and restore the
next snapshot through a separate PR. Align bilingual documentation via docs PR.
