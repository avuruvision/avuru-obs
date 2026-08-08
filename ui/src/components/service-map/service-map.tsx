"use client";

import { useEffect, useImperativeHandle, useRef } from "react";
import { useRouter } from "next/navigation";
import cytoscape, { type Core, type LayoutOptions } from "cytoscape";
import fcose from "cytoscape-fcose";
import { cn } from "@/lib/cn";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

let layoutRegistered = false;
function ensureLayout() {
  if (!layoutRegistered) {
    cytoscape.use(fcose);
    layoutRegistered = true;
  }
}

// Resolve Avuru Gold tokens from the live theme (daisyUI CSS vars) so the graph
// follows light/dark — never hardcode hex (agent_docs/ui_patterns.md).
function themeColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) =>
    cs.getPropertyValue(name).trim() || fallback;
  return {
    primary: v("--color-primary", "#c9a96a"),
    error: v("--color-error", "#f87171"),
    warning: v("--color-warning", "#f59e0b"),
    success: v("--color-success", "#34d399"),
    surface: v("--color-base-200", "#0f1729"),
    base100: v("--color-base-100", "#0b1120"),
    text: v("--color-base-content", "#e8e5dc"),
    edge: v("--color-neutral", "#33415580"),
  };
}

// Compact mode (the Dashboard's band 2) is the SAME graph at overview scale:
// smaller nodes, smaller labels, a tighter simulation and a shorter canvas.
// Deliberately a mode rather than a second component — the v0.5 plan's W7
// restyle then lands on the full map and the Dashboard at once.
const scale = (compact: boolean) => ({
  node: compact ? "mapData(rate, 0, 10, 14, 40)" : "mapData(rate, 0, 10, 22, 64)",
  fontSize: compact ? 9 : 11,
  labelMargin: compact ? 3 : 5,
  edge: compact ? "mapData(calls, 0, 50, 0.8, 3.2)" : "mapData(calls, 0, 50, 1.2, 5)",
});

// carbon (module green) layers a border-color intensity scale onto the nodes.
// When it is off the chain is byte-identical to the pre-green map — the overlay
// must be invisible and inert on a non-green install.
function applyStyle(cy: Core, carbon = false, compact = false) {
  const c = themeColors();
  const s = scale(compact);
  const withNodes = cy
    .style()
    .resetToDefault()
    .selector("node")
    .style({
      "background-color": c.primary,
      label: "data(label)",
      color: c.text,
      "font-size": s.fontSize,
      "text-valign": "bottom",
      "text-margin-y": s.labelMargin,
      // Keep labels legible where they sit over edges/nodes.
      "text-background-color": c.base100,
      "text-background-opacity": 0.7,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      width: s.node,
      height: s.node,
      "border-width": 2,
      "border-color": c.surface,
    })
    .selector("node[error > 0]")
    .style({ "background-color": c.error });
  // Carbon border scale (low→high gCO2e): success → warning → error. Applied
  // after the fill selectors so a node can read error (fill) and carbon (border)
  // at once. Only bucketed nodes carry the `carbon` attribute, so nodes without
  // energy keep the default border.
  const withCarbon = carbon
    ? withNodes
        .selector("node[carbon = 0]")
        .style({ "border-color": c.success, "border-width": 3 })
        .selector("node[carbon = 1]")
        .style({ "border-color": c.warning, "border-width": 3 })
        .selector("node[carbon = 2]")
        .style({ "border-color": c.error, "border-width": 3 })
    : withNodes;
  withCarbon
    .selector("edge")
    .style({
      width: s.edge,
      "line-color": c.edge,
      "target-arrow-color": c.edge,
      "target-arrow-shape": "triangle",
      "arrow-scale": 0.9,
      "curve-style": "bezier",
      opacity: 0.85,
    })
    // Network-health amber (high RTT or failed connections) — before the error
    // selector so trace-error red always wins when an edge is both.
    .selector("edge[health > 0]")
    .style({ "line-color": c.warning, "target-arrow-color": c.warning, "line-style": "dashed" })
    .selector("edge[error > 0]")
    .style({ "line-color": c.error, "target-arrow-color": c.error, "line-style": "solid" })
    .update();
}

// carbonBucket maps a node's gCO2e into the 3-step border scale, relative to
// the heaviest node in view so the lens is meaningful at any absolute scale.
function carbonBucket(gco2e: number, max: number): 0 | 1 | 2 {
  if (max <= 0) return 0;
  const r = gco2e / max;
  return r < 1 / 3 ? 0 : r < 2 / 3 ? 1 : 2;
}

// nodeEnergyTooltip is the hover text a node gains under the carbon overlay:
// the service name plus its energy and carbon for the window.
function nodeEnergyTooltip(label: string, wh: number, gco2e: number): string {
  return `${label} · ${wh.toFixed(1)} Wh · ${gco2e.toFixed(2)} gCO2e`;
}

// An edge is network-unhealthy when RTT p95 is high or any connections failed.
const RTT_UNHEALTHY_MS = 100;
function edgeUnhealthy(e: ServiceEdge): boolean {
  return (e.rttMs ?? 0) > RTT_UNHEALTHY_MS || (e.failedConnections ?? 0) > 0;
}

// edgeTooltip is the hover text for an edge: call volume plus any network
// health OBI measured for the connection.
function edgeTooltip(e: ServiceEdge): string {
  const parts = [`${e.source} → ${e.target}`, `${e.calls} calls`];
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% errors`);
  if (e.rttMs) parts.push(`RTT p95 ${e.rttMs.toFixed(0)}ms`);
  if (e.failedConnections) parts.push(`${e.failedConnections} failed conns`);
  if (e.bytes) parts.push(`${formatBytes(e.bytes)}`);
  return parts.join(" · ");
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// Label-aware fcose options: without nodeDimensionsIncludeLabels the
// simulation ignores label width and stacks the names on top of each other.
// The initial layout is deterministic (no animation, seeded positions); a
// re-layout randomizes the seed to escape a tangled local minimum, animated
// so the untangle reads as movement, not a flash.
const layoutOptions = (animate: boolean, compact = false) =>
  ({
    name: "fcose",
    quality: "proof",
    animate,
    randomize: animate,
    padding: compact ? 26 : 60,
    nodeDimensionsIncludeLabels: true,
    nodeSeparation: compact ? 95 : 170,
    idealEdgeLength: compact ? 95 : 170,
    nodeRepulsion: compact ? 5200 : 9000,
  }) as unknown as LayoutOptions;

export interface ServiceMapHandle {
  relayout: () => void;
}

// Service-map graph. Nodes = services (sized by request rate, red = errors);
// edges = caller→callee call volume derived from trace spans. Click a node to
// open its traces.
export function ServiceMap({
  services,
  edges,
  handleRef,
  carbon = false,
  compact = false,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  handleRef?: React.Ref<ServiceMapHandle>;
  // carbon (module green) turns on the gCO2e border scale + node energy
  // tooltip. Default off: on a non-green install the map is byte-unchanged.
  carbon?: boolean;
  // compact renders the overview-scale variant for the Dashboard's band 2.
  // Default off: the full Service Map screen is unchanged by this prop.
  compact?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const router = useRouter();

  useImperativeHandle(handleRef, () => ({
    relayout: () => cyRef.current?.layout(layoutOptions(true, compact)).run(),
  }));

  useEffect(() => {
    if (!ref.current) return;
    ensureLayout();
    const names = new Set(services.map((s) => s.name));
    // Heaviest node in view anchors the relative carbon scale (green only).
    const maxGco2e = carbon ? Math.max(0, ...services.map((s) => s.gco2e ?? 0)) : 0;
    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...services.map((s) => ({
          // Carbon fields are added ONLY under the overlay, so a non-green node
          // carries the exact same data as before.
          data: {
            id: s.name,
            label: s.name,
            error: s.errorRate,
            rate: s.ratePerSec,
            ...(carbon && s.gco2e !== undefined
              ? { wh: s.wh, gco2e: s.gco2e, carbon: carbonBucket(s.gco2e, maxGco2e) }
              : {}),
          },
        })),
        // Only edges between known nodes (a callee may have aged out of the window).
        ...edges
          .filter((e) => names.has(e.source) && names.has(e.target) && e.source !== e.target)
          .map((e) => ({
            data: {
              id: `${e.source}->${e.target}`,
              source: e.source,
              target: e.target,
              calls: e.calls,
              error: e.errorRate,
              health: edgeUnhealthy(e) ? 1 : 0,
              tooltip: edgeTooltip(e),
            },
          })),
      ],
      layout: layoutOptions(false, compact),
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    applyStyle(cy, carbon, compact);
    cy.on("tap", "node", (e) => {
      router.push(`/traces?service=${encodeURIComponent(e.target.id())}&tab=traces`);
    });
    // Edge hover tooltip: call volume + any OBI network health for the edge.
    const tip = tooltipRef.current;
    cy.on("mouseover", "edge", (evt) => {
      if (!tip) return;
      tip.textContent = String(evt.target.data("tooltip") ?? "");
      const p = evt.renderedPosition;
      if (p) {
        tip.style.left = `${p.x}px`;
        tip.style.top = `${p.y}px`;
      }
      tip.style.opacity = "1";
    });
    const hideTip = () => {
      if (tip) tip.style.opacity = "0";
    };
    cy.on("mouseout", "edge", hideTip);
    cy.on("pan zoom drag", hideTip);
    // Node energy tooltip — only under the carbon overlay, so a non-green map
    // registers no node hover handler at all.
    if (carbon) {
      cy.on("mouseover", "node", (evt) => {
        if (!tip) return;
        const d = evt.target.data();
        if (d.wh === undefined || d.gco2e === undefined) return;
        tip.textContent = nodeEnergyTooltip(String(d.label), Number(d.wh), Number(d.gco2e));
        const p = evt.renderedPosition;
        if (p) {
          tip.style.left = `${p.x}px`;
          tip.style.top = `${p.y}px`;
        }
        tip.style.opacity = "1";
      });
      cy.on("mouseout", "node", hideTip);
    }
    cyRef.current = cy;
    return () => {
      cy.destroy();
      cyRef.current = null;
    };
  }, [services, edges, router, carbon, compact]);

  // Re-theme the graph when the user toggles light/dark. Re-subscribes on a
  // carbon change so the re-style always reflects the live overlay state.
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
      {/* Edge hover tooltip (calls + OBI network health). Positioned by the
          cytoscape mouseover handler; pointer-events-none so it never eats hover. */}
      <div
        ref={tooltipRef}
        className="pointer-events-none absolute z-10 max-w-xs -translate-x-1/2 -translate-y-full rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs text-base-content opacity-0 shadow-md transition-opacity"
        style={{ left: 0, top: 0 }}
      />
    </div>
  );
}
