"use client";

import { useEffect, useRef } from "react";
import cytoscape from "cytoscape";
import { serviceColor } from "@/lib/trace";
import type { Span, TraceResponse } from "@/lib/api-types";

// Resolve daisyUI tokens to concrete colors — cytoscape doesn't evaluate var().
function themeColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) =>
    cs.getPropertyValue(name).trim() || fallback;
  return {
    text: v("--color-base-content", "#e8e5dc"),
    edge: v("--color-neutral", "#334155"),
    error: v("--color-error", "#f87171"),
    base100: v("--color-base-100", "#0b1120"),
  };
}

// Node-link diagram of the call tree — Jaeger's "Trace Graph". Spans collapse
// into (service, operation) nodes; edges follow parent→child links with counts.
export function TraceGraph({ trace }: { trace: TraceResponse }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!ref.current) return;
    const byId = new Map<string, Span>(trace.spans.map((s) => [s.spanId, s]));
    const key = (s: Span) => `${s.service}\n${s.operation}`;

    const nodes = new Map<string, { service: string; operation: string; count: number; error: number }>();
    const edges = new Map<string, { source: string; target: string; count: number }>();
    for (const s of trace.spans) {
      const k = key(s);
      const n = nodes.get(k) ?? { service: s.service, operation: s.operation, count: 0, error: 0 };
      n.count++;
      if (s.statusCode === "Error") n.error++;
      nodes.set(k, n);

      const parent = byId.get(s.parentSpanId);
      if (parent && key(parent) !== k) {
        const ek = `${key(parent)}=>${k}`;
        const e = edges.get(ek) ?? { source: key(parent), target: k, count: 0 };
        e.count++;
        edges.set(ek, e);
      }
    }

    const c = themeColors();
    const cy = cytoscape({
      container: ref.current,
      elements: [
        ...[...nodes.entries()].map(([id, n]) => ({
          data: {
            id,
            label: `${n.service}\n${n.operation}${n.count > 1 ? ` ×${n.count}` : ""}`,
            color: serviceColor(n.service),
            error: n.error,
          },
        })),
        ...[...edges.values()].map((e) => ({
          data: { id: `${e.source}~${e.target}`, source: e.source, target: e.target, label: String(e.count) },
        })),
      ],
      layout: { name: "breadthfirst", directed: true, padding: 30, spacingFactor: 1.3 },
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    cy.style()
      .resetToDefault()
      .selector("node")
      .style({
        "background-color": "data(color)",
        label: "data(label)",
        "text-wrap": "wrap",
        "text-max-width": "130",
        color: c.text,
        "font-size": 10,
        "text-valign": "bottom",
        "text-margin-y": 4,
        width: 20,
        height: 20,
        "border-width": 2,
        "border-color": c.base100,
      })
      .selector("node[error > 0]")
      .style({ "border-color": c.error })
      .selector("edge")
      .style({
        width: 1.2,
        "line-color": c.edge,
        "target-arrow-color": c.edge,
        "target-arrow-shape": "triangle",
        "arrow-scale": 0.8,
        "curve-style": "bezier",
        label: "data(label)",
        "font-size": 9,
        color: c.text,
        "text-background-color": c.base100,
        "text-background-opacity": 0.7,
        "text-background-padding": "2px",
      })
      .update();

    return () => cy.destroy();
  }, [trace]);

  return (
    <div ref={ref} className="h-[62vh] w-full rounded-lg border border-neutral bg-base-200" />
  );
}
