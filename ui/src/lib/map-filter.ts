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
