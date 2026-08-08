"use client";

import Link from "next/link";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ServiceMap } from "@/components/service-map/service-map";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

// Band 2, left column: the Service Map at overview scale. This is the SAME
// component the /service-map screen renders, in its compact mode — so the v0.5
// plan's W7 restyle improves both surfaces at once instead of leaving a second
// topology behind to catch up.
export function TopologyCard({
  services,
  edges,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
}) {
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Topology</CardTitle>
        <div className="flex items-center gap-3">
          <span className="text-xs text-base-content/45">
            {services.length} services · {edges.length} edges
          </span>
          <Link href="/service-map" className="text-xs text-primary hover:underline">
            Open full map →
          </Link>
        </div>
      </CardHeader>
      <div className="px-3 pb-3">
        <ServiceMap services={services} edges={edges} compact />
      </div>
    </Card>
  );
}
