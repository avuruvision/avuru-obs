"use client";

import { useMemo, useState } from "react";
import { serviceColor } from "@/lib/trace";
import type { FlameNode } from "@/lib/api-types";

const ROW_H = 22;
const MIN_FRACTION = 0.002; // skip slivers below 0.2% of the zoomed width

interface Bar {
  node: FlameNode;
  depth: number;
  left: number; // fraction of the zoomed root
  width: number;
}

// Value-proportional icicle with click-to-zoom. Distinct from the trace
// flamegraph (time-positioned): here widths are aggregate sample shares.
// Same absolute-positioned-div technique, still no chart dependency.
export function FlameGraph({ root }: { root: FlameNode }) {
  const [zoomNode, setZoomNode] = useState<FlameNode | null>(null);
  const zoom = zoomNode ?? root;

  const bars = useMemo(() => {
    const out: Bar[] = [];
    const walk = (node: FlameNode, depth: number, left: number, width: number) => {
      if (width < MIN_FRACTION || depth > 63) return;
      out.push({ node, depth, left, width });
      let childLeft = left;
      for (const child of node.children ?? []) {
        const w = zoom.value ? child.value / zoom.value : 0;
        walk(child, depth + 1, childLeft, w);
        childLeft += w;
      }
    };
    walk(zoom, 0, 0, 1);
    return out;
  }, [zoom]);

  const maxDepth = bars.reduce((m, b) => Math.max(m, b.depth), 0);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-xs text-base-content/55">
        <button
          type="button"
          onClick={() => setZoomNode(null)}
          className="rounded bg-base-300 px-2 py-0.5 hover:bg-base-300/70 disabled:opacity-40"
          disabled={!zoomNode}
        >
          reset zoom
        </button>
        <span>
          {zoom === root ? "all stacks" : zoom.name} · {zoom.value.toLocaleString()} samples ·
          click a frame to zoom
        </span>
      </div>
      <div
        className="relative w-full overflow-hidden rounded-lg bg-base-300/30"
        style={{ height: (maxDepth + 1) * ROW_H }}
        role="figure"
        aria-label="CPU flame graph"
      >
        {bars.map((b, i) => (
          <button
            key={`${b.depth}:${i}`}
            type="button"
            onClick={() => setZoomNode(b.node === root ? null : b.node)}
            title={`${b.node.name} — ${b.node.value.toLocaleString()} samples (${(b.width * 100).toFixed(1)}%)`}
            className="absolute overflow-hidden truncate rounded-sm border border-base-100/60 px-1 text-left font-mono text-[10px] leading-5 text-base-100 hover:brightness-110"
            style={{
              top: b.depth * ROW_H,
              left: `${b.left * 100}%`,
              width: `${b.width * 100}%`,
              height: ROW_H - 2,
              backgroundColor: b.depth === 0 ? "var(--color-neutral)" : serviceColor(b.node.name),
            }}
          >
            {b.width > 0.03 ? b.node.name : ""}
          </button>
        ))}
      </div>
    </div>
  );
}
