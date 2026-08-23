"use client";

import { useEffect, useImperativeHandle, useRef } from "react";
import { useRouter } from "next/navigation";
import cytoscape, { type Core, type LayoutOptions, type NodeSingular } from "cytoscape";
import fcose from "cytoscape-fcose";
import { cn } from "@/lib/cn";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { applyStyle } from "./graph-style";
import { buildElements, nodeEnergyTooltip } from "./graph-elements";
import { clearFocus, focusNeighbourhood } from "./graph-focus";

let layoutRegistered = false;
function ensureLayout() {
  if (!layoutRegistered) {
    cytoscape.use(fcose);
    layoutRegistered = true;
  }
}

// Shared by the layout padding, the compact auto-fit, and the handle's fit()
// so the compact/full split lives in one place instead of three.
const fitPadding = (compact: boolean) => (compact ? 26 : 60);

// Label-aware fcose options: without nodeDimensionsIncludeLabels the simulation
// ignores label width and stacks the names on top of each other. The initial
// layout is deterministic (no animation, seeded positions); a re-layout
// randomizes the seed to escape a tangled local minimum, animated so the
// untangle reads as movement, not a flash.
const layoutOptions = (animate: boolean, compact = false) =>
  ({
    name: "fcose",
    quality: "proof",
    animate,
    randomize: animate,
    padding: fitPadding(compact),
    nodeDimensionsIncludeLabels: true,
    nodeSeparation: compact ? 95 : 170,
    idealEdgeLength: compact ? 95 : 170,
    nodeRepulsion: compact ? 5200 : 9000,
  }) as unknown as LayoutOptions;

export interface ServiceMapHandle {
  relayout: () => void;
  fit: () => void;
  zoomBy: (factor: number) => void;
}

const EMPTY_HEALTH: Map<string, ServiceHealth> = new Map();

// Service-map graph. Nodes = services (sized by request rate, ring = health
// status); edges = caller→callee call volume derived from trace spans. Hover a
// node to focus its neighbourhood and reveal per-edge rpm/latency; click a
// service to open its traces (a virtual target has none to open).
export function ServiceMap({
  services,
  edges,
  windowMs,
  health = EMPTY_HEALTH,
  handleRef,
  carbon = false,
  compact = false,
  healthEnabled = false,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  /** Window length, for turning edge call counts into rpm. */
  windowMs: number;
  /** Per-service health for the rings. Empty on an install without the module,
   *  which leaves every ring neutral. */
  health?: Map<string, ServiceHealth>;
  handleRef?: React.Ref<ServiceMapHandle>;
  // carbon (module green) turns on the gCO2e halo + node energy tooltip.
  // Default off: on a non-green install the map is byte-unchanged.
  carbon?: boolean;
  // compact renders the overview-scale variant for the Dashboard's band 2.
  compact?: boolean;
  // Whether the service-health module is on. Must be explicit rather than
  // inferred from `health` being non-empty: the module can be on and simply
  // have returned no members yet, which would misread as "off". Drives the
  // module-off error-ring fallback in graph-elements.ts.
  healthEnabled?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const router = useRouter();

  useImperativeHandle(
    handleRef,
    () => ({
      relayout: () => cyRef.current?.layout(layoutOptions(true, compact)).run(),
      fit: () => cyRef.current?.fit(undefined, fitPadding(compact)),
      zoomBy: (factor: number) => {
        const cy = cyRef.current;
        if (!cy) return;
        cy.zoom({
          level: cy.zoom() * factor,
          renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
        });
      },
    }),
    [compact],
  );

  useEffect(() => {
    if (!ref.current) return;
    ensureLayout();
    const cy = cytoscape({
      container: ref.current,
      elements: buildElements({ services, edges, health, windowMs, carbon, healthEnabled }),
      layout: layoutOptions(false, compact),
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    applyStyle(cy, carbon, compact);
    // Compact sits in a short card, so a wide estate hangs off the edges unless
    // it is fitted. Safe to call straight away: the initial layout runs with
    // animate:false, so positions are already final here.
    if (compact) cy.fit(undefined, fitPadding(compact));

    cy.on("tap", "node", (e) => {
      // A virtual target has no service to open: it never sent a span, and
      // filtering traces by its system would match on the trace's ROOT span
      // and quietly return a different set of traces than the one asked for.
      // Doing nothing is the honest answer until there is a per-target view.
      if (e.target.data("virtual")) return;
      router.push(`/traces?service=${encodeURIComponent(e.target.id())}&tab=traces`);
    });

    const tip = tooltipRef.current;
    const showTip = (text: string, p?: { x: number; y: number }) => {
      if (!tip || !text) return;
      tip.textContent = text;
      if (p) {
        tip.style.left = `${p.x}px`;
        tip.style.top = `${p.y}px`;
      }
      tip.style.opacity = "1";
    };
    const hideTip = () => {
      if (tip) tip.style.opacity = "0";
    };

    // Edge hover tooltip: rpm, this path's p95, and any OBI network health.
    cy.on("mouseover", "edge", (evt) => {
      showTip(String(evt.target.data("tooltip") ?? ""), evt.renderedPosition);
    });
    cy.on("mouseout", "edge", hideTip);
    cy.on("pan zoom drag", hideTip);

    // Node hover drives the focus, and (under the carbon lens only) the energy
    // tooltip. Both live in one handler so they cannot fight over mouseover.
    cy.on("mouseover", "node", (evt) => {
      focusNeighbourhood(cy, evt.target as NodeSingular);
      const d = evt.target.data();
      // A virtual target's label drops its URI scheme to keep the graph
      // readable; the hover is where the full identity comes back, along with
      // the reminder that every number on it was measured at the caller.
      if (d.virtual) {
        showTip(String(d.tooltip ?? ""), evt.renderedPosition);
        return;
      }
      if (!carbon) return;
      if (d.wh === undefined || d.gco2e === undefined) return;
      showTip(
        nodeEnergyTooltip(String(d.label), Number(d.wh), Number(d.gco2e)),
        evt.renderedPosition,
      );
    });
    cy.on("mouseout", "node", () => {
      clearFocus(cy);
      hideTip();
    });

    cyRef.current = cy;
    return () => {
      cy.destroy();
      cyRef.current = null;
    };
  }, [services, edges, health, windowMs, router, carbon, compact, healthEnabled]);

  // Re-theme the graph when the user toggles light/dark.
  useEffect(() => {
    const obs = new MutationObserver(() => {
      if (cyRef.current) applyStyle(cyRef.current, carbon, compact);
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => obs.disconnect();
  }, [carbon, compact]);

  return (
    <div className="relative">
      <div
        ref={ref}
        data-testid={compact ? "service-map-compact" : "service-map"}
        className={cn(
          // Compact sits inside the Dashboard's Card, which already draws the
          // surface — a second border there would read as a box in a box.
          "w-full rounded-xl",
          compact ? "h-75" : "h-[70vh] border border-neutral bg-base-200",
        )}
      />
      {/* Hover tooltip (edge detail, or node energy under the carbon lens).
          Positioned by the cytoscape handlers; pointer-events-none so it never
          eats hover. */}
      <div
        ref={tooltipRef}
        className="pointer-events-none absolute z-10 max-w-xs -translate-x-1/2 -translate-y-full rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs text-base-content opacity-0 shadow-md transition-opacity"
        style={{ left: 0, top: 0 }}
      />
    </div>
  );
}
