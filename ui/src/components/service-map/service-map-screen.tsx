"use client";

import { Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { ServiceMap } from "./service-map";

export function ServiceMapScreen() {
  const { time } = useTimeRange();
  const { data, isLoading } = useServiceMapData(time);

  if (isLoading) return <CenteredSpinner />;
  const services = data?.services ?? [];

  if (!services.length) {
    return (
      <EmptyState icon={MapIcon} title="No services yet">
        The service map draws itself from the services sending OTLP — point an
        OTel SDK at the gateway and they appear here. Call edges (topology) light
        up from eBPF flows in a later milestone.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs text-base-content/55">
        {services.length} services · nodes sized by request rate, red = errors ·
        click a node for its traces. Call edges arrive with eBPF flows.
      </p>
      <ServiceMap services={services} />
    </div>
  );
}
