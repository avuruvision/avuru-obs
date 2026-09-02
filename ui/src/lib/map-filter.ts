import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

export interface MapFilters {
  /** Case-insensitive substring on the service name. */
  q?: string;
  /** Keep only degraded + down. */
  problemsOnly?: boolean;
  /** Keep only members of this service group. */
  group?: string;
  /** Keep one service and its 1-hop neighbourhood, by exact name. */
  focus?: string;
}

export function hasActiveFilter(f: MapFilters): boolean {
  return Boolean(f.q?.trim() || f.problemsOnly || f.group || f.focus?.trim());
}

// Whether any part of this dependency's traffic went through the mesh.
export function viaMesh(e: ServiceEdge): boolean {
  return (e.viaTransport?.length ?? 0) > 0;
}

// Removes the mesh-carried traffic from an edge set, for the view that draws
// the hops themselves.
//
// Subtracting rather than filtering matters for the pair that talks BOTH ways —
// some calls through the mesh, some around it, which a mesh exclusion produces.
// Dropping such an edge outright would erase a real direct dependency; keeping
// it whole would draw its mesh half twice. What is left is exactly what was
// observed directly, and an edge with nothing left disappears.
export function withoutCollapsed(edges: ServiceEdge[]): ServiceEdge[] {
  return edges.flatMap((e) => {
    const meshCalls = e.collapsedCalls ?? 0;
    // Untouched edges pass through byte-identical — including flow-derived
    // ones, which legitimately carry zero calls and must not be swept up by a
    // "nothing left" rule that was never about them.
    if (meshCalls === 0) return [e];
    const calls = Math.max(0, e.calls - meshCalls);
    // No calls were observed directly: what is left is the mesh's hops, which
    // the caller is about to draw. Unless the kernel also saw bytes on this
    // pair — that observation is independent of any span and survives as the
    // flow edge it always was.
    if (calls === 0 && !e.bytes) return [];
    const errorCount = Math.max(0, e.errorCount - (e.collapsedErrorCount ?? 0));
    return [
      {
        ...e,
        calls,
        errorCount,
        errorRate: calls > 0 ? errorCount / calls : 0,
        viaTransport: undefined,
        collapsedCalls: undefined,
        collapsedErrorCount: undefined,
      },
    ];
  });
}

// Splits the mesh out of the map.
//
// A service-mesh sidecar, waypoint or ingress gateway carries other services'
// traffic. It emits spans and exchanges bytes exactly like an application, so
// left alone the map draws `app → proxy → app` as two application
// dependencies and asserts a relationship between two services that never talk
// to each other. Dropping the transport nodes drops those hops with them.
//
// This is a VIEW decision, not a data one: the hub still reports every node and
// every edge, and the toolbar toggle brings them straight back with no refetch.
// Kept beside filterMap for the same reason — this is what the user sees, and
// it should be readable without knowing the graph library.
//
// It also owns the DOUBLE-COUNT RULE. A collapsed edge (`app → app`, recovered
// by walking the trace across the proxy) and the two hops it was recovered from
// describe the SAME requests. Drawing both would treble the apparent traffic,
// so the toggle SWAPS representations rather than accumulating them: hops
// hidden → collapsed edges shown; hops shown → collapsed edges hidden.
export function splitInfrastructure(
  services: ServiceStats[],
  edges: ServiceEdge[],
  show: boolean,
): { services: ServiceStats[]; edges: ServiceEdge[]; hidden: number } {
  const transport = services.filter((s) => s.role === "transport");
  if (show) {
    // The mesh is on screen, so its hops are the truth being told — the
    // recovered calls would be drawn a second time as `app → app`.
    return { services, edges: withoutCollapsed(edges), hidden: 0 };
  }
  if (transport.length === 0) return { services, edges, hidden: 0 };

  // Drop exactly the edges that touch a node being HIDDEN — that edge IS the
  // hop — and not every edge whose endpoint is unknown. The difference matters:
  // an edge can legitimately point at something absent from the services list
  // (a workload the sensor saw traffic to that never sent telemetry), and
  // keeping it is what lets the renderer draw that peer instead of deleting the
  // connection.
  const removed = new Set(transport.map((s) => s.name));
  return {
    services: services.filter((s) => s.role !== "transport"),
    edges: edges.filter((e) => !removed.has(e.source) && !removed.has(e.target)),
    hidden: transport.length,
  };
}

// Splits the derived dependencies out of the map.
//
// A virtual target — a database, cache or broker — is real infrastructure, but
// it is inferred from its callers' exit spans rather than observed directly, and
// on a chatty estate it can outnumber the applications. This is the same VIEW
// decision splitInfrastructure makes: the hub reports every node, and the
// toolbar toggle brings them back with no refetch.
//
// `count` is the number of virtual targets present BEFORE the split, so the
// count line can say how many are hidden as easily as how many are shown.
export function splitVirtual(
  services: ServiceStats[],
  edges: ServiceEdge[],
  show: boolean,
): { services: ServiceStats[]; edges: ServiceEdge[]; count: number } {
  const count = services.filter((s) => s.role === "virtual").length;
  if (show || count === 0) return { services, edges, count };

  // Same rule as splitInfrastructure: drop the edges touching the nodes being
  // hidden — a dangling edge draws to nothing — and leave the rest alone.
  const removed = new Set(
    services.filter((s) => s.role === "virtual").map((s) => s.name),
  );
  return {
    services: services.filter((s) => s.role !== "virtual"),
    edges: edges.filter((e) => !removed.has(e.source) && !removed.has(e.target)),
    count,
  };
}

// Narrows the graph to the services a filter keeps, then drops every edge with
// an endpoint that went away — a dangling edge would draw to nothing.
//
// Kept pure and cytoscape-free on purpose: this is the logic that decides what
// the user sees, and it should be readable without knowing the graph library.
export function filterMap(
  services: ServiceStats[],
  edges: ServiceEdge[],
  filters: MapFilters,
  health: Map<string, ServiceHealth>,
): { services: ServiceStats[]; edges: ServiceEdge[] } {
  if (!hasActiveFilter(filters)) return { services, edges };

  const q = filters.q?.trim().toLowerCase();
  // "Show me THIS service on the map" is a neighbourhood, not a name match: the
  // focused service plus everything on an edge to or from it. Matching the name
  // alone — which is what the service page used to link to — keeps the node and
  // drops every edge it has, landing the reader on an isolated dot.
  //
  // One hop, and one hop only: it is the same neighbourhood hovering a node
  // lights up, and the answer to "what does this service touch" rather than
  // "what does the estate look like from here".
  const focus = filters.focus?.trim();
  const neighbourhood = focus
    ? new Set(
        edges
          .filter((e) => e.source === focus || e.target === focus)
          .flatMap((e) => [e.source, e.target])
          .concat(focus),
      )
    : undefined;
  const kept = services.filter((s) => {
    if (neighbourhood && !neighbourhood.has(s.name)) return false;
    if (q && !s.name.toLowerCase().includes(q)) return false;
    const h = health.get(s.name);
    // A service with no health entry is unknown, not healthy — so it is never
    // what "problems only" keeps, and it belongs to no group.
    if (filters.problemsOnly && h?.status !== "degraded" && h?.status !== "down") return false;
    if (filters.group && h?.group !== filters.group) return false;
    return true;
  });

  const names = new Set(kept.map((s) => s.name));
  return {
    services: kept,
    edges: edges.filter((e) => names.has(e.source) && names.has(e.target)),
  };
}
