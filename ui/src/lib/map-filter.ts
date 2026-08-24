import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

export interface MapFilters {
  /** Case-insensitive substring on the service name. */
  q?: string;
  /** Keep only degraded + down. */
  problemsOnly?: boolean;
  /** Keep only members of this service group. */
  group?: string;
}

export function hasActiveFilter(f: MapFilters): boolean {
  return Boolean(f.q?.trim() || f.problemsOnly || f.group);
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
export function splitInfrastructure(
  services: ServiceStats[],
  edges: ServiceEdge[],
  show: boolean,
): { services: ServiceStats[]; edges: ServiceEdge[]; hidden: number } {
  const transport = services.filter((s) => s.role === "transport");
  if (show || transport.length === 0) return { services, edges, hidden: 0 };

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
  const kept = services.filter((s) => {
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
