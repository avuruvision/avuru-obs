// Shared trace-tree helpers used by every trace view (timeline, spans table,
// flamegraph, statistics, graph). Keeps span-tree building and service coloring
// in one place so the views stay consistent.

import type { Span } from "@/lib/api-types";

// The hue band services are drawn from: [40, 330), avoiding the red/pink error
// band at both ends ([0,40) and (330,360]). A service whose name hashed red
// used to make an all-OK trace read as failing.
const HUE_START = 40;
const HUE_SPAN = 290;

// Hues are QUANTIZED to this many steps rather than taken anywhere in the band.
// A continuous hash puts two services three degrees apart as readily as thirty,
// and three degrees is not a distinction — it is a rendering artifact. It was
// tolerable while services only ever appeared as thin waterfall bars; adjacent
// treemap blocks made it a defect.
//
// Eighteen steps put any two DIFFERENT steps at least 16 degrees apart, which
// is comfortably above the just-noticeable difference at this lightness and
// chroma, while keeping the palette large enough that same-hue pairs stay rare.
//
// The cost is real and deliberate: two services can now share a hue EXACTLY.
// That cannot be probed away the way series-color.ts resolves its categorical
// slots, because probing assigns from the set being rendered, and this hash has
// to answer per name — a service must be the same colour in the waterfall as in
// the treemap, on screens that never see the same set. So the trade is between
// two kinds of collision, and shared beats near: a shared colour reads as "these
// two look alike", while three degrees reads as "these two are the same and the
// renderer is wobbling".
const HUE_STEPS = 18;

// FNV-1a, 32-bit — the same hash series-color.ts uses, and for the same reason:
// small, dependency-free, and stable across renders, reloads and browsers, so a
// service keeps its colour.
//
// The previous hash reduced modulo 360 on every character, which capped the
// state at nine bits and cost it most of its avalanche: names sharing a suffix
// landed near each other rather than anywhere. Quantizing a weak hash would
// just concentrate the collisions, so this had to be fixed first.
function hashName(name: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

// Stable service hue from a name hash — consistent colors across all screens.
export function serviceHue(name: string): number {
  const step = hashName(name) % HUE_STEPS;
  return HUE_START + Math.round((step * HUE_SPAN) / HUE_STEPS);
}

export function serviceColor(name: string): string {
  return `oklch(0.65 0.13 ${serviceHue(name)})`;
}

export interface TraceRow {
  span: Span;
  depth: number;
  hasChildren: boolean;
  descendantCount: number; // total spans under this one (for "+N" collapse chips)
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

// buildRows flattens the span tree depth-first into render rows. Spans whose
// id is in `collapsed` still get a row, but their subtree is skipped.
export function buildRows(spans: Span[], collapsed?: ReadonlySet<string>): TraceRow[] {
  const byParent = childrenByParent(spans);
  const counts = new Map<string, number>();
  const countDescendants = (id: string): number => {
    const cached = counts.get(id);
    if (cached !== undefined) return cached;
    let n = 0;
    for (const c of byParent.get(id) ?? []) n += 1 + countDescendants(c.spanId);
    counts.set(id, n);
    return n;
  };
  const rows: TraceRow[] = [];
  const walk = (parent: string, depth: number) => {
    for (const s of byParent.get(parent) ?? []) {
      const hasChildren = (byParent.get(s.spanId) ?? []).length > 0;
      rows.push({ span: s, depth, hasChildren, descendantCount: countDescendants(s.spanId) });
      if (!collapsed?.has(s.spanId)) walk(s.spanId, depth + 1);
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
