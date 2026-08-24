"use client";

import { Maximize2, Shuffle, ZoomIn, ZoomOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { HealthGroup } from "@/lib/api-types";
import type { MapFilters } from "@/lib/map-filter";
import type { MapGrouping } from "./graph-elements";

// Filters + view controls. Every filter is URL state (a map URL must be
// pasteable into Slack — agent_docs/ui_patterns.md), so this component owns no
// state: it reads the current filters and reports changes upward.
//
// The status and group filters exist ONLY when the service-health module is on,
// because both read the rollup that module produces. The mesh and dependency
// toggles appear only when there IS a mesh or a dependency — a checkbox that
// would change nothing is noise.
export function MapToolbar({
  filters,
  groups,
  healthEnabled,
  canCarbon,
  carbon,
  includeAux,
  showInfra,
  hasInfra,
  showVirtual,
  hasVirtual,
  grouping,
  edgeLabels,
  zoomPercent,
  onFilters,
  onCarbon,
  onIncludeAux,
  onShowInfra,
  onShowVirtual,
  onGrouping,
  onEdgeLabels,
  onZoomIn,
  onZoomOut,
  onFit,
  onRelayout,
}: {
  filters: MapFilters;
  groups: HealthGroup[];
  healthEnabled: boolean;
  canCarbon: boolean;
  carbon: boolean;
  includeAux: boolean;
  showInfra: boolean;
  hasInfra: boolean;
  showVirtual: boolean;
  hasVirtual: boolean;
  grouping: MapGrouping;
  edgeLabels: boolean;
  /** Current zoom as a whole percentage (100 = actual size). */
  zoomPercent: number;
  onFilters: (next: MapFilters) => void;
  onCarbon: (on: boolean) => void;
  onIncludeAux: (on: boolean) => void;
  onShowInfra: (on: boolean) => void;
  onShowVirtual: (on: boolean) => void;
  onGrouping: (next: MapGrouping) => void;
  onEdgeLabels: (on: boolean) => void;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  onRelayout: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <input
        type="search"
        aria-label="Filter services"
        placeholder="Filter services…"
        value={filters.q ?? ""}
        onChange={(e) => onFilters({ ...filters, q: e.target.value })}
        className="h-7 w-44 rounded-lg border border-neutral bg-base-100 px-2 text-xs text-base-content placeholder:text-base-content/40 focus-visible:outline-2 focus-visible:outline-primary"
      />

      {healthEnabled && (
        <>
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
            <input
              type="checkbox"
              checked={Boolean(filters.problemsOnly)}
              onChange={(e) => onFilters({ ...filters, problemsOnly: e.target.checked })}
              className="accent-warning"
            />
            Problems only
          </label>
          <select
            aria-label="Filter by group"
            value={filters.group ?? ""}
            onChange={(e) => onFilters({ ...filters, group: e.target.value || undefined })}
            className="h-7 rounded-lg border border-neutral bg-base-100 px-2 text-xs text-base-content"
          >
            <option value="">All groups</option>
            {groups.map((g) => (
              <option key={g.name} value={g.name}>
                {g.name}
              </option>
            ))}
          </select>
        </>
      )}

      {canCarbon && (
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
          <input
            type="checkbox"
            checked={carbon}
            onChange={(e) => onCarbon(e.target.checked)}
            className="accent-success"
          />
          Carbon
        </label>
      )}

      <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
        <input
          type="checkbox"
          checked={includeAux}
          onChange={(e) => onIncludeAux(e.target.checked)}
          className="accent-primary"
        />
        Show auxiliary requests
      </label>

      {hasVirtual && (
        <label
          className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70"
          title="Databases, caches and message brokers send no telemetry of their own. They are derived from the exit spans of the services calling them, so everything shown about them is measured at the caller."
        >
          <input
            type="checkbox"
            checked={showVirtual}
            onChange={(e) => onShowVirtual(e.target.checked)}
            className="accent-primary"
          />
          Databases &amp; queues
        </label>
      )}

      {hasInfra && (
        <label
          className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70"
          title="Mesh sidecars, waypoint proxies and ingress gateways carry other services' traffic. Their edges are hops between two services, not a dependency between them."
        >
          <input
            type="checkbox"
            checked={showInfra}
            onChange={(e) => onShowInfra(e.target.checked)}
            className="accent-primary"
          />
          Show mesh &amp; gateways
        </label>
      )}

      <label
        className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70"
        title="Label every edge with its volume — requests per minute, or bytes for a connection with no traced calls behind it."
      >
        <input
          type="checkbox"
          checked={edgeLabels}
          onChange={(e) => onEdgeLabels(e.target.checked)}
          className="accent-primary"
        />
        Edge volume
      </label>

      <label className="flex items-center gap-1.5 text-xs text-base-content/70">
        Group by
        <select
          aria-label="Group nodes by"
          value={grouping}
          onChange={(e) => onGrouping(e.target.value as MapGrouping)}
          className="h-7 rounded-lg border border-neutral bg-base-100 px-2 text-xs text-base-content"
        >
          <option value="none">Nothing</option>
          <option value="namespace">Namespace</option>
          {/* A service group only exists where the health module computes one. */}
          {healthEnabled && <option value="group">Service group</option>}
        </select>
      </label>

      <div className="ml-auto flex items-center gap-1">
        <span
          data-testid="map-zoom"
          className="mr-1 tabular-nums text-xs text-base-content/45"
          title="Current zoom"
        >
          {zoomPercent}%
        </span>
        <Button variant="ghost" size="icon" aria-label="Zoom in" onClick={onZoomIn}>
          <ZoomIn className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Zoom out" onClick={onZoomOut}>
          <ZoomOut className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Fit to view" onClick={onFit}>
          <Maximize2 className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="sm" aria-label="Re-run layout" onClick={onRelayout}>
          <Shuffle className="h-3.5 w-3.5" /> Re-layout
        </Button>
      </div>
    </div>
  );
}
