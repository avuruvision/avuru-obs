"use client";

import Link from "next/link";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ServiceMap } from "@/components/service-map/service-map";
import { useTimeRange } from "@/hooks/use-time-range";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { splitInfrastructure } from "@/lib/map-filter";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

// Band 2, left column: the Service Map at overview scale. This is the SAME
// component the /service-map screen renders, in its compact mode — so the v0.5
// W7 restyle improved both surfaces at once.
//
// The health read costs nothing here: band 1 already issues this exact query
// with the same key (summary-band.tsx), so the rings come out of the cache.
//
// Mesh proxies and gateways are dropped with no way to bring them back: at
// overview scale a transport hop drawn as a dependency is pure
// misinformation, and the full map — one click away — owns the toggle.
export function TopologyCard({
  services,
  edges,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
}) {
  const { windowMs, time } = useTimeRange();
  const healthEnabled = useModuleEnabled("service-health");
  const { byService } = useServiceHealthStatus(time, false, healthEnabled);
  const app = splitInfrastructure(services, edges, false);

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Topology</CardTitle>
        <div className="flex items-center gap-3">
          <span className="text-xs text-base-content/45">
            {app.services.length} services · {app.edges.length} edges
          </span>
          <Link href="/service-map" className="text-xs text-primary hover:underline">
            Open full map →
          </Link>
        </div>
      </CardHeader>
      <div className="px-3 pb-3">
        <ServiceMap
          services={app.services}
          edges={app.edges}
          windowMs={windowMs}
          health={byService}
          compact
          healthEnabled={healthEnabled}
        />
      </div>
    </Card>
  );
}
