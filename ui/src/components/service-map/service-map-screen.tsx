"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { Crosshair, Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import {
  filterMap,
  hasActiveFilter,
  splitInfrastructure,
  splitVirtual,
  viaMesh,
  type MapFilters,
} from "@/lib/map-filter";
import { ROLE_PEER, withUndetectedPeers } from "@/lib/map-peers";
import { ServiceMap, type ServiceMapHandle } from "./service-map";
import type { MapGrouping } from "./graph-elements";
import { MapToolbar } from "./map-toolbar";
import { MapLegend } from "./map-legend";

export function ServiceMapScreen() {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  // Mesh proxies and gateways are off by default: their edges are transport
  // hops, not dependencies. URL state, so a map with the mesh shown is still a
  // pasteable link.
  const showInfra = get("infra") === "true";
  // Derived dependencies are ON by default — a database the map already knows
  // about is the point of drawing it. So the URL carries the OPT-OUT, and a
  // link with no `virtual` param shows the whole picture.
  const showVirtual = get("virtual") !== "false";
  // Boundaries are URL state like every other map control, so a link that says
  // "look at the storefront namespace" arrives grouped.
  const grouping = (get("groupBy") ?? "none") as MapGrouping;
  const edgeLabels = get("edgeLabels") === "true";
  const { data, isLoading } = useServiceMapData(time, includeAux);
  const greenEnabled = useModuleEnabled("green");
  const healthEnabled = useModuleEnabled("service-health");
  // Aux stays excluded from the health read, matching the Health screen's
  // default — the two screens must not disagree about a group's status.
  const { byService, groups } = useServiceHealthStatus(time, false, healthEnabled);
  const mapRef = useRef<ServiceMapHandle>(null);
  // Whole percent, so a re-render only happens when the readout would actually
  // change. Starts at 100 and is corrected as soon as the graph reports its own
  // fit — the layout fits on first run, so the real opening value is rarely 100.
  const [zoomPercent, setZoomPercent] = useState(100);
  // Stable identity: this goes into the graph effect's dependencies, and a new
  // function each render would tear down and rebuild cytoscape on every zoom.
  const onZoomPercent = useCallback((pct: number) => setZoomPercent(pct), []);

  const filters: MapFilters = useMemo(
    () => ({
      q: get("q"),
      problemsOnly: get("problems") === "true",
      group: get("group"),
      // Arriving from a service page: keep that service and its neighbours,
      // rather than filtering the map down to the one name.
      focus: get("focus"),
    }),
    [get],
  );

  const all = useMemo(() => data?.services ?? [], [data]);
  const allEdges = useMemo(() => data?.edges ?? [], [data]);
  // Infrastructure comes out first, so "filtered from N" counts applications
  // rather than including nodes the user never asked to see.
  const {
    services: afterInfra,
    edges: edgesAfterInfra,
    hidden: hiddenInfra,
  } = useMemo(
    () => splitInfrastructure(all, allEdges, showInfra),
    [all, allEdges, showInfra],
  );
  const {
    services: afterVirtual,
    edges,
    count: virtualCount,
  } = useMemo(
    () => splitVirtual(afterInfra, edgesAfterInfra, showVirtual),
    [afterInfra, edgesAfterInfra, showVirtual],
  );
  // LAST of the three, and it has to be: the two splits above drop nodes AND
  // the edges that touched them, so running peer synthesis before either would
  // resurrect a hidden mesh proxy as an "undetected peer".
  const services = useMemo(
    () => withUndetectedPeers(afterVirtual, edges),
    [afterVirtual, edges],
  );
  const shown = useMemo(
    () => filterMap(services, edges, filters, byService),
    [services, edges, filters, byService],
  );
  // Applications and derived dependencies are counted apart: they are not the
  // same kind of thing, and folding a database into "services" would silently
  // inflate a number people compare against their own deployment count.
  const shownApps = shown.services.filter(
    (s) => s.role !== "virtual" && s.role !== ROLE_PEER,
  ).length;
  const shownVirtual = shown.services.filter((s) => s.role === "virtual").length;
  const shownPeers = shown.services.filter((s) => s.role === ROLE_PEER).length;
  // "filtered from N" has to compare like with like: N is the applications a
  // cleared filter would show, not applications plus dependencies.
  const totalApps = services.filter(
    (s) => s.role !== "virtual" && s.role !== ROLE_PEER,
  ).length;
  // Flow-derived edges carry no call volume by construction, so counting them
  // as calls overstates what the map actually observed.
  const callEdges = shown.edges.filter((e) => e.calls > 0).length;
  const flowEdges = shown.edges.length - callEdges;
  // Dependencies that exist only because the hub walked a trace across a mesh
  // proxy. Counted apart because they are the answer to "why does my meshed
  // cluster finally have edges" — and because a reader who trusts a map should
  // be told which edges were reconstructed rather than observed head-on.
  const meshEdges = shown.edges.filter((e) => viaMesh(e)).length;

  const setFilters = (next: MapFilters) =>
    setMany({
      q: next.q || undefined,
      problems: next.problemsOnly ? "true" : undefined,
      group: next.group || undefined,
      focus: next.focus || undefined,
    });

  if (isLoading) return <CenteredSpinner />;

  // The carbon lens is offered ONLY when green runs AND the map actually
  // carries energy (the hub stamps wh/gco2e only then). When it isn't offered,
  // carbon stays off regardless of a stale ?carbon= — the map renders
  // byte-unchanged.
  const canCarbon = greenEnabled && all.some((s) => s.wh !== undefined);
  const carbon = canCarbon && get("carbon") === "true";

  if (!all.length) {
    return (
      <EmptyState icon={MapIcon} title="No services yet">
        The service map draws itself from the services sending OTLP — point an
        OTel SDK at the gateway and they appear here. Call edges come from trace
        spans; the eBPF sensor adds the connections traces never see.
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
        showInfra={showInfra}
        hasInfra={hiddenInfra > 0 || showInfra}
        showVirtual={showVirtual}
        hasVirtual={virtualCount > 0}
        grouping={grouping}
        edgeLabels={edgeLabels}
        zoomPercent={zoomPercent}
        onFilters={setFilters}
        onCarbon={(on) => setMany({ carbon: on ? "true" : undefined })}
        onIncludeAux={(on) => setMany({ includeAux: on ? "true" : undefined })}
        onShowInfra={(on) => setMany({ infra: on ? "true" : undefined })}
        onShowVirtual={(on) => setMany({ virtual: on ? undefined : "false" })}
        onGrouping={(next) => setMany({ groupBy: next === "none" ? undefined : next })}
        onEdgeLabels={(on) => setMany({ edgeLabels: on ? "true" : undefined })}
        onZoomIn={() => mapRef.current?.zoomBy(1.25)}
        onZoomOut={() => mapRef.current?.zoomBy(0.8)}
        onFit={() => mapRef.current?.fit()}
        onRelayout={() => mapRef.current?.relayout()}
      />

      {filters.focus && (
        // A narrowed map has to say so, and say how to leave. Without this the
        // reader has a map missing most of their estate and no way to tell
        // whether that is a filter or the truth.
        <div
          data-testid="map-focus"
          className="flex flex-wrap items-center gap-2 text-xs text-base-content/60"
        >
          <span className="inline-flex items-center gap-1.5 rounded-full border border-primary/40 bg-base-200 px-2 py-0.5">
            <Crosshair className="h-3 w-3 text-primary" aria-hidden />
            Neighbourhood of{" "}
            <span className="font-mono text-base-content">{filters.focus}</span>
          </span>
          <button
            type="button"
            onClick={() => setMany({ focus: undefined })}
            className="text-primary hover:underline"
          >
            show the whole map
          </button>
        </div>
      )}

      <p data-testid="map-count" className="text-xs text-base-content/55">
        {shownApps} services · {callEdges} call edges
        {meshEdges > 0 && ` · ${meshEdges} through the mesh`}
        {flowEdges > 0 && ` · ${flowEdges} network ${flowEdges === 1 ? "flow" : "flows"}`}
        {shownVirtual > 0 && ` · ${shownVirtual} ${shownVirtual === 1 ? "dependency" : "dependencies"}`}
        {shownPeers > 0 && ` · ${shownPeers} undetected ${shownPeers === 1 ? "peer" : "peers"}`}
        {!showVirtual &&
          virtualCount > 0 &&
          ` · ${virtualCount} ${virtualCount === 1 ? "dependency" : "dependencies"} hidden`}
        {hiddenInfra > 0 &&
          ` · ${hiddenInfra} mesh/gateway node${hiddenInfra === 1 ? "" : "s"} hidden`}
        {hasActiveFilter(filters) && ` · filtered from ${totalApps}`} · click
        a service for its traces.
      </p>

      <MapLegend
        health={healthEnabled}
        carbon={carbon}
        infra={showInfra}
        mesh={meshEdges > 0}
        virtual={shownVirtual > 0}
        peers={shownPeers > 0}
        grouping={grouping}
      />

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
          focus={filters.focus}
          carbon={carbon}
          healthEnabled={healthEnabled}
          grouping={grouping}
          edgeLabels={edgeLabels}
          onZoomPercent={onZoomPercent}
        />
      )}
    </div>
  );
}
