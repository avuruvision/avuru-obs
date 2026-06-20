"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import cytoscape, { type Core, type LayoutOptions } from "cytoscape";
import fcose from "cytoscape-fcose";
import type { ServiceStats } from "@/lib/api-types";

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
    text: v("--color-base-content", "#e8e5dc"),
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
      "text-margin-y": 4,
      width: "mapData(rate, 0, 10, 22, 64)",
      height: "mapData(rate, 0, 10, 22, 64)",
      "border-width": 2,
      "border-color": c.surface,
    })
    .selector("node[error > 0]")
    .style({ "background-color": c.error })
    .update();
}

// Service-map graph. Nodes = services (sized by request rate, red = errors);
// click a node to open its traces. Edges arrive with eBPF flows (later).
export function ServiceMap({ services }: { services: ServiceStats[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const router = useRouter();

  useEffect(() => {
    if (!ref.current) return;
    ensureLayout();
    const cy = cytoscape({
      container: ref.current,
      elements: services.map((s) => ({
        data: { id: s.name, label: s.name, error: s.errorRate, rate: s.ratePerSec },
      })),
      layout: { name: "fcose", animate: false, padding: 30 } as unknown as LayoutOptions,
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
  }, [services, router]);

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
