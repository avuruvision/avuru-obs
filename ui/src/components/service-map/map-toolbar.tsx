"use client";

import { Maximize2, Shuffle, ZoomIn, ZoomOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { HealthGroup } from "@/lib/api-types";
import type { MapFilters } from "@/lib/map-filter";

// Filters + view controls. Every filter is URL state (a map URL must be
// pasteable into Slack — agent_docs/ui_patterns.md), so this component owns no
// state: it reads the current filters and reports changes upward.
//
// The status and group filters exist ONLY when the service-health module is on,
// because both read the rollup that module produces. The mesh toggle appears
// only when there IS a mesh — a checkbox that would change nothing is noise.
export function MapToolbar({
  filters,
  groups,
  healthEnabled,
  canCarbon,
  carbon,
  includeAux,
  showInfra,
  hasInfra,
  onFilters,
  onCarbon,
  onIncludeAux,
  onShowInfra,
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
  onFilters: (next: MapFilters) => void;
  onCarbon: (on: boolean) => void;
  onIncludeAux: (on: boolean) => void;
  onShowInfra: (on: boolean) => void;
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

      <div className="ml-auto flex items-center gap-1">
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
