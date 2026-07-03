"use client";

import { useEffect, useRef } from "react";
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
    .selector("edge[error > 0]")
    .style({ "line-color": c.error, "target-arrow-color": c.error })
    .update();
}

// Service-map graph. Nodes = services (sized by request rate, red = errors);
// edges = caller→callee call volume derived from trace spans. Click a node to
// open its traces.
export function ServiceMap({
  services,
  edges,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
}) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const router = useRouter();

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
            },
          })),
      ],
      // Label-aware layout: without nodeDimensionsIncludeLabels the simulation
      // ignores label width and stacks the names on top of each other.
      layout: {
        name: "fcose",
        quality: "proof",
        animate: false,
        padding: 60,
        nodeDimensionsIncludeLabels: true,
        nodeSeparation: 140,
        idealEdgeLength: 130,
        nodeRepulsion: 6500,
      } as unknown as LayoutOptions,
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    applyStyle(cy);
    cy.on("tap", "node", (e) => {
      router.push(`/traces?service=${encodeURIComponent(e.target.id())}&tab=traces`);
    });
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
    <div
      ref={ref}
      className="h-[70vh] w-full rounded-xl border border-neutral bg-base-200"
    />
  );
}
