"use client";

import Link from "next/link";
import { Boxes, Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServicesData } from "@/hooks/use-services-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { ServicesTable } from "./services-table";

// Auto-discovered service inventory with RED stats. Row click drills into the
// service's traces; the map link shows the same services as topology.
export function ServicesScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data, isLoading } = useServicesData(time, includeAux);

  if (isLoading) return <CenteredSpinner />;
  const services = data?.services ?? [];

  if (!services.length) {
    return (
      <EmptyState icon={Boxes} title="No services yet">
        Services appear as soon as telemetry flows — the sensor discovers them
        zero-code on Kubernetes, or point an OTel SDK at the gateway.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-base-content/55">
          {services.length} services in this window · click a row for its
          traces ·{" "}
          <Link href="/service-map" className="inline-flex items-center gap-1 text-primary hover:underline">
            <MapIcon className="h-3 w-3" aria-hidden /> view as topology
          </Link>
        </p>
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
          <input
            type="checkbox"
            checked={includeAux}
            onChange={(e) => setMany({ includeAux: e.target.checked ? "true" : undefined })}
            className="accent-primary"
          />
          Show auxiliary requests
        </label>
      </div>
      <ServicesTable services={services} />
    </div>
  );
}
