"use client";

import { useEffect, useImperativeHandle, useRef } from "react";
import { useRouter } from "next/navigation";
import cytoscape, { type Core, type LayoutOptions } from "cytoscape";
import fcose from "cytoscape-fcose";
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
    surface: v("--color-base-200", "#0f1729"),
    base100: v("--color-base-100", "#0b1120"),
    text: v("--color-base-content", "#e8e5dc"),
    edge: v("--color-neutral", "#33415580"),
  };
}

function applyStyle(cy: Core) {
  const c = themeColors();
  cy.style()
    .resetToDefault()
    .selector("node")
    .style({
      "background-color": c.primary,
      label: "data(label)",
      color: c.text,
      "font-size": 11,
      "text-valign": "bottom",
      "text-margin-y": 5,
      // Keep labels legible where they sit over edges/nodes.
      "text-background-color": c.base100,
      "text-background-opacity": 0.7,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      width: "mapData(rate, 0, 10, 22, 64)",
      height: "mapData(rate, 0, 10, 22, 64)",
      "border-width": 2,
      "border-color": c.surface,
    })
    .selector("node[error > 0]")
    .style({ "background-color": c.error })
    .selector("edge")
    .style({
      width: "mapData(calls, 0, 50, 1.2, 5)",
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
const layoutOptions = (animate: boolean) =>
  ({
    name: "fcose",
    quality: "proof",
    animate,
    randomize: animate,
    padding: 60,
    nodeDimensionsIncludeLabels: true,
    nodeSeparation: 170,
    idealEdgeLength: 170,
    nodeRepulsion: 9000,
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
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  handleRef?: React.Ref<ServiceMapHandle>;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const router = useRouter();

  useImperativeHandle(handleRef, () => ({
    relayout: () => cyRef.current?.layout(layoutOptions(true)).run(),
  }));

  useEffect(() => {
    if (!ref.current) return;
    ensureLayout();
    const names = new Set(services.map((s) => s.name));
    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...services.map((s) => ({
          data: { id: s.name, label: s.name, error: s.errorRate, rate: s.ratePerSec },
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
      layout: layoutOptions(false),
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    applyStyle(cy);
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
    cyRef.current = cy;
    return () => {
      cy.destroy();
      cyRef.current = null;
    };
  }, [services, edges, router]);

  // Re-theme the graph when the user toggles light/dark.
  useEffect(() => {
    const obs = new MutationObserver(() => {
      if (cyRef.current) applyStyle(cyRef.current);
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => obs.disconnect();
  }, []);

  return (
    <div className="relative">
      <div
        ref={ref}
        className="h-[70vh] w-full rounded-xl border border-neutral bg-base-200"
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
