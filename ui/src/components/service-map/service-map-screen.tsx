"use client";

import { useRef } from "react";
import { Map as MapIcon, Shuffle } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { ServiceMap, type ServiceMapHandle } from "./service-map";

export function ServiceMapScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data, isLoading } = useServiceMapData(time, includeAux);
  const greenEnabled = useModuleEnabled("green");
  const mapRef = useRef<ServiceMapHandle>(null);

  if (isLoading) return <CenteredSpinner />;
  const services = data?.services ?? [];
  const edges = data?.edges ?? [];

  // The carbon lens is offered ONLY when green runs AND the map actually carries
  // energy (the hub stamps wh/gco2e only then). When it isn't offered, carbon
  // stays off regardless of a stale ?carbon= — the map renders byte-unchanged.
  const canCarbon = greenEnabled && services.some((s) => s.wh !== undefined);
  const carbon = canCarbon && get("carbon") === "true";

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
        <div className="flex items-center gap-3">
          {canCarbon && (
            <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
              <input
                type="checkbox"
                checked={carbon}
                onChange={(e) => setMany({ carbon: e.target.checked ? "true" : undefined })}
                className="accent-success"
              />
              Carbon
            </label>
          )}
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
            <input
              type="checkbox"
              checked={includeAux}
              onChange={(e) => setMany({ includeAux: e.target.checked ? "true" : undefined })}
              className="accent-primary"
            />
            Show auxiliary requests
          </label>
          <Button
            variant="ghost"
            size="sm"
            aria-label="Re-run layout"
            onClick={() => mapRef.current?.relayout()}
          >
            <Shuffle className="h-3.5 w-3.5" /> Re-layout
          </Button>
        </div>
      </div>
      {carbon && (
        <p className="text-xs text-base-content/45">
          Carbon lens on · node border = gCO2e (green → amber → red); hover a node
          for its Wh and gCO2e.
        </p>
      )}
      <ServiceMap services={services} edges={edges} handleRef={mapRef} carbon={carbon} />
    </div>
  );
}
