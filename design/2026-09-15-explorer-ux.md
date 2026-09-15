# Explorer — the connected application workspace

Status: Accepted direction by the maintainer; implementation tracked in #232.

## Context and decision

The Atlas landing introduces observability through service relationships. The
application currently opens on a dashboard and navigating a graph node leaves
the map immediately. Explorer makes that relationship the starting point and
keeps an inspector beside the graph until the reader chooses a deeper signal.

Use sage surfaces and forest/lime accents through the shared semantic themes.
Light is the new-install default; a stored light/dark choice still wins. Status
colors remain independent from branding. Existing routes, permissions, project
selection, capability gating and the static SPA architecture remain intact.

## Implementation plan

1. Apply Explorer tokens, typography, cards and responsive shell across all
   application routes. Keep every enabled navigation destination reachable on
   narrow screens through a keyboard-accessible navigation disclosure.
2. Open new visits on the service map. Add a page introduction, live counts,
   graph/inspector composition and a keyboard-accessible service selector.
3. Store selection separately from graph filtering in `?selected=`. Clicking
   a graph node selects it; the inspector links to details/traces/logs using
   existing routes and global time/project context. Unknown or filtered-out
   selections are explicitly identified, never replaced with unrelated data.
4. Render inferred dependencies and unresolved peers as such. Own-service RED
   and health are not available on these nodes; show callers and caller-side
   operations instead. Network-only edges never acquire request latency.
5. Preserve grouping, zoom, transport collapse, carbon gating, mTLS and edge
   provenance. Selection must not re-layout the graph. Respect reduced motion.
6. Make the empty map teach eBPF/OTLP connection; distinguish loading, failed
   reads, empty collection and filters that match nothing.
7. Add authenticated browser regressions for onboarding, selection, context,
   themes and mobile navigation alongside existing golden-screen coverage.
8. Run `make check`, `make e2e-ui`, chart checks and relevant release gates.
   Merge via PR after required checks pass. Finalize the next minor version
   via release PR, publish a signed tag, verify workflow/artifacts, then restore
   the next snapshot through a separate PR. No direct push to main.

## Alternatives

Focus provides a dense trace-first investigation workspace; Pulse provides a
summary-first overview. Explorer was chosen to make the first map the shortest
path to value. Existing trace, dashboard and module screens remain available
and receive the shared theme; the prototype's fixed datasets are never shipped.

## Verification and rollback

Verify the real seeded auth-enabled stack, both themes, keyboard selection,
mobile navigation, reloadable URLs, module-off behavior and missing measurements.
Run existing map, service, trace, logs, profiling, project and auth coverage.
The change needs no database migration or backend contract change. A normal
revert PR restores the prior UI; telemetry and collection configuration persist.
