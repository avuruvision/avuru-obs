"use client";

import { useMemo } from "react";
import { Waypoints } from "lucide-react";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServiceMap } from "@/components/service-map/service-map";
import { roleShapeWord } from "@/components/service-map/role-shapes";
import { splitInfrastructure } from "@/lib/map-filter";
import type { MeshProxy, ServiceEdge, ServiceStats } from "@/lib/api-types";
import { roleLabel, rolesPresent } from "./mesh-roles";

// The mesh as a graph, with its hops left IN.
//
// The service map exists to take them out: it draws dependencies, and a hop is
// not a dependency. That is the right default there and it means the estate has
// no picture of the mesh itself — a waypoint sitting between two services is
// exactly what the map deletes.
//
// Scoped to the mesh and one step around it, which is what makes this worth
// having next to the map's own infrastructure toggle: on a 200-service estate
// that toggle adds the proxies to everything, and this shows the proxies and
// who touches them.
export function MeshGraph({
  services,
  edges,
  windowMs,
  proxies,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  windowMs: number;
  // The proxy rows, for their roles: the map's own data has none, and the
  // graph draws each role as its own shape (service-map/role-shapes.ts).
  proxies: MeshProxy[];
}) {
  const scoped = useMemo(() => {
    // show=true keeps the transport nodes AND drops the collapsed app-to-app
    // edges recovered from them — drawing both would count the same requests
    // twice, which is the rule that file owns.
    const { services: all, edges: withHops } = splitInfrastructure(services, edges, true);

    const transport = new Set(
      all.filter((s) => s.role === "transport").map((s) => s.name),
    );
    if (transport.size === 0) return { services: [], edges: [] };

    // One step out: a proxy with no neighbours tells you nothing, and the whole
    // estate tells you nothing about the mesh.
    const keep = new Set(transport);
    for (const e of withHops) {
      if (transport.has(e.source)) keep.add(e.target);
      if (transport.has(e.target)) keep.add(e.source);
    }
    return {
      services: all.filter((s) => keep.has(s.name)),
      edges: withHops.filter((e) => keep.has(e.source) && keep.has(e.target)),
    };
  }, [services, edges]);

  // name → role, memoised: it is a dependency of the graph's build effect.
  // A proxy with no role gets no entry, so its node keeps the plain diamond.
  const meshRoles = useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of proxies) if (p.role) m[p.name] = p.role;
    return m;
  }, [proxies]);

  // Legend lines for the roles actually DRAWN — a role in the proxy list whose
  // node is not on this graph would explain a shape nobody can see.
  const legend = useMemo(() => {
    const drawn = new Set(
      scoped.services.filter((s) => s.role === "transport").map((s) => s.name),
    );
    return rolesPresent(proxies.filter((p) => drawn.has(p.name)));
  }, [scoped.services, proxies]);

  if (scoped.services.length === 0) {
    return (
      <EmptyState icon={Waypoints} title="Nothing to draw for this mesh">
        No workload in this window is classified as transport, so there are no
        hops to show. The classification is correctable per install through the
        hub&apos;s topology config.
      </EmptyState>
    );
  }

  return (
    <Card className="overflow-hidden p-0">
      <div data-testid="mesh-graph" className="h-[560px]">
        <ServiceMap
          services={scoped.services}
          edges={scoped.edges}
          windowMs={windowMs}
          grouping="namespace"
          edgeLabels
          meshRoles={meshRoles}
        />
      </div>
      {legend.length > 0 && (
        <div
          data-testid="mesh-graph-legend"
          className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-neutral px-4 py-2 text-xs text-base-content/55"
        >
          <span className="text-base-content/45">shape:</span>
          {legend.map((r) => (
            <span key={r}>
              {roleShapeWord(r)} = {roleLabel(r)}
            </span>
          ))}
        </div>
      )}
    </Card>
  );
}
