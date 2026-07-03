// Shared trace-tree helpers used by every trace view (timeline, spans table,
// flamegraph, statistics, graph). Keeps span-tree building and service coloring
// in one place so the views stay consistent.

import type { Span } from "@/lib/api-types";

// Stable service hue from a name hash — consistent colors across all screens.
export function serviceHue(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360;
  return h;
}

export function serviceColor(name: string): string {
  return `oklch(0.65 0.13 ${serviceHue(name)})`;
}

export interface TraceRow {
  span: Span;
  depth: number;
}

// childrenByParent maps a parent span id → its direct children, ordered by start
// time. Spans whose parent is missing are treated as roots (partial traces
// happen); they live under the "" key.
export function childrenByParent(spans: Span[]): Map<string, Span[]> {
  const ids = new Set(spans.map((s) => s.spanId));
  const byParent = new Map<string, Span[]>();
  for (const s of spans) {
    const parent = ids.has(s.parentSpanId) ? s.parentSpanId : "";
    const list = byParent.get(parent) ?? [];
    list.push(s);
    byParent.set(parent, list);
  }
  for (const list of byParent.values()) {
    list.sort((a, b) => a.startTime.localeCompare(b.startTime));
  }
  return byParent;
}

// buildRows flattens the span tree depth-first into render rows.
export function buildRows(spans: Span[]): TraceRow[] {
  const byParent = childrenByParent(spans);
  const rows: TraceRow[] = [];
  const walk = (parent: string, depth: number) => {
    for (const s of byParent.get(parent) ?? []) {
      rows.push({ span: s, depth });
      walk(s.spanId, depth + 1);
    }
  };
  walk("", 0);
  return rows;
}

// selfTimeMs is a span's duration minus the time covered by its direct children
// (clamped at 0). Good enough for hotspot coloring; ignores child overlap.
export function selfTimeMs(span: Span, children: Span[]): number {
  const childTotal = children.reduce((sum, c) => sum + c.durationMs, 0);
  return Math.max(span.durationMs - childTotal, 0);
}
