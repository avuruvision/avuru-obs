import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

// The client-side role for a peer the renderer synthesized. The hub never sends
// it: it is a statement about what the RENDERER could not resolve, not about
// what the cluster is, so it is derived here rather than invented server-side.
export const ROLE_PEER = "peer";

// Recovers the far end of a connection nobody instrumented.
//
// The hub reports every edge it observed, including ones whose endpoint never
// sent a span — an eBPF flow to a workload with no telemetry of its own. The
// renderer used to DROP those edges, because a graph edge needs two nodes and
// only one existed. So the one screen built to show connections was silently
// deleting the connections it could least afford to lose: the parts of the
// estate nobody has instrumented yet.
//
// Each unresolved endpoint becomes a node of its own, carrying no metrics
// because we have none — it is drawn as an outline, and every number about it
// is absent rather than zero.
//
// Deliberately NOT toggleable. A peer exists only because an edge points at it,
// so peers cannot outnumber edges, and a switch that hides them would restore
// the exact defect this fixes.
export function withUndetectedPeers(
  services: ServiceStats[],
  edges: ServiceEdge[],
): ServiceStats[] {
  const known = new Set(services.map((s) => s.name));
  const unresolved = new Set<string>();
  for (const e of edges) {
    if (!known.has(e.source)) unresolved.add(e.source);
    if (!known.has(e.target)) unresolved.add(e.target);
  }
  if (unresolved.size === 0) return services;

  // Sorted so the graph is built in a stable order — the layout is randomized,
  // but the element list should not be a source of churn on its own.
  const peers: ServiceStats[] = [...unresolved].sort().map((name) => ({
    name,
    spanCount: 0,
    ratePerSec: 0,
    errorRate: 0,
    p50Ms: 0,
    p95Ms: 0,
    p99Ms: 0,
    role: ROLE_PEER,
  }));
  return [...services, ...peers];
}
