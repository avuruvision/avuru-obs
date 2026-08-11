"use client";

import { useMemo, useRef } from "react";
import { Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { filterMap, hasActiveFilter, type MapFilters } from "@/lib/map-filter";
import { ServiceMap, type ServiceMapHandle } from "./service-map";
import { MapToolbar } from "./map-toolbar";
import { MapLegend } from "./map-legend";

export function ServiceMapScreen() {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data, isLoading } = useServiceMapData(time, includeAux);
  const greenEnabled = useModuleEnabled("green");
  const healthEnabled = useModuleEnabled("service-health");
  // Aux stays excluded from the health read, matching the Health screen's
  // default — the two screens must not disagree about a group's status.
  const { byService, groups } = useServiceHealthStatus(time, false, healthEnabled);
  const mapRef = useRef<ServiceMapHandle>(null);

  const filters: MapFilters = useMemo(
    () => ({
      q: get("q"),
      problemsOnly: get("problems") === "true",
      group: get("group"),
    }),
    [get],
  );

  const services = useMemo(() => data?.services ?? [], [data]);
  const edges = useMemo(() => data?.edges ?? [], [data]);
  const shown = useMemo(
    () => filterMap(services, edges, filters, byService),
    [services, edges, filters, byService],
  );

  const setFilters = (next: MapFilters) =>
    setMany({
      q: next.q || undefined,
      problems: next.problemsOnly ? "true" : undefined,
      group: next.group || undefined,
    });

  if (isLoading) return <CenteredSpinner />;

  // The carbon lens is offered ONLY when green runs AND the map actually
  // carries energy (the hub stamps wh/gco2e only then). When it isn't offered,
  // carbon stays off regardless of a stale ?carbon= — the map renders
  // byte-unchanged.
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
      <MapToolbar
        filters={filters}
        groups={groups}
        healthEnabled={healthEnabled}
        canCarbon={canCarbon}
        carbon={carbon}
        includeAux={includeAux}
        onFilters={setFilters}
        onCarbon={(on) => setMany({ carbon: on ? "true" : undefined })}
        onIncludeAux={(on) => setMany({ includeAux: on ? "true" : undefined })}
        onZoomIn={() => mapRef.current?.zoomBy(1.25)}
        onZoomOut={() => mapRef.current?.zoomBy(0.8)}
        onFit={() => mapRef.current?.fit()}
        onRelayout={() => mapRef.current?.relayout()}
      />

      <p data-testid="map-count" className="text-xs text-base-content/55">
        {shown.services.length} services · {shown.edges.length} call edges
        {hasActiveFilter(filters) && ` · filtered from ${services.length}`} · click
        a node for its traces.
      </p>

      <MapLegend health={healthEnabled} carbon={carbon} />

      {shown.services.length === 0 ? (
        <EmptyState icon={MapIcon} title="No services match">
          No service in this window matches the current filter. Clear it, or
          widen the time range.
        </EmptyState>
      ) : (
        <ServiceMap
          services={shown.services}
          edges={shown.edges}
          windowMs={windowMs}
          health={byService}
          handleRef={mapRef}
          carbon={carbon}
          healthEnabled={healthEnabled}
        />
      )}
    </div>
  );
}
