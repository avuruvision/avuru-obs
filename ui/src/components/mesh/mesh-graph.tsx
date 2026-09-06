"use client";

import { useMemo } from "react";
import { Waypoints } from "lucide-react";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServiceMap } from "@/components/service-map/service-map";
import { splitInfrastructure } from "@/lib/map-filter";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

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
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  windowMs: number;
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
        />
      </div>
    </Card>
  );
}
