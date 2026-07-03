"use client";

import { Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { ServiceMap } from "./service-map";

export function ServiceMapScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data, isLoading } = useServiceMapData(time, includeAux);

  if (isLoading) return <CenteredSpinner />;
  const services = data?.services ?? [];
  const edges = data?.edges ?? [];

  if (!services.length) {
    return (
      <EmptyState icon={MapIcon} title="No services yet">
        The service map draws itself from the services sending OTLP — point an
        OTel SDK at the gateway and they appear here. Call edges are derived from
        trace spans; eBPF flows will enrich them in a later milestone.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-base-content/55">
          {services.length} services · {edges.length} call edges · nodes sized by
          request rate, red = errors · click a node for its traces.
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
      <ServiceMap services={services} edges={edges} />
    </div>
  );
}
