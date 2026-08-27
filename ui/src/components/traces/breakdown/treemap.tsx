"use client";

import { useMemo } from "react";
import { squarify } from "@/lib/treemap";
import { formatMs, formatPercent } from "@/lib/format";
import type { BreakdownGroup } from "@/lib/api-types";

// The drawing surface. A fixed viewBox scaled to the container: the layout is
// computed once in these units and the browser handles the responsiveness, so
// tile proportions can never drift between breakpoints.
const VIEW_W = 1000;
const VIEW_H = 420;
const GAP = 2; // surface gap between fills, so adjacent tiles never bleed

export interface TreemapDatum extends BreakdownGroup {
  color: string;
  label: string;
}

// Treemap of one breakdown dimension: area is the weight, colour is identity.
//
// Health is NOT encoded as a hue here - that would put two meanings on one
// channel and leave a reader unable to tell "the orange service" from "the
// service in trouble". Failures get a channel of their own instead: a rule
// along the bottom of the tile, as wide a fraction of it as the requests that
// did not succeed.
export function Treemap({
  data,
  weight,
  onSelect,
}: {
  data: TreemapDatum[];
  weight: "count" | "time";
  onSelect?: (key: string) => void;
}) {
  const tiles = useMemo(
    () =>
      squarify(
        data.map((d) => ({ key: d.key, value: weight === "count" ? d.count : d.durationMsSum })),
        VIEW_W,
        VIEW_H,
      ),
    [data, weight],
  );
  const byKey = useMemo(() => new Map(data.map((d) => [d.key, d])), [data]);
  const total = useMemo(
    () => data.reduce((sum, d) => sum + (weight === "count" ? d.count : d.durationMsSum), 0),
    [data, weight],
  );

  if (!tiles.length) return null;

  return (
    <svg
      viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
      width="100%"
      className="h-[320px] w-full"
      role="img"
      aria-label="Traffic breakdown treemap"
    >
      {tiles.map((tile) => {
        const datum = byKey.get(tile.key);
        if (!datum) return null;
        const w = Math.max(0, tile.w - GAP);
        const h = Math.max(0, tile.h - GAP);
        const share = total > 0 ? tile.value / total : 0;
        // Labels are drawn only where they fit. A clipped label is worse than
        // none: the hover carries the full identity either way.
        const showLabel = w > 68 && h > 30;
        const showValue = w > 68 && h > 48;
        const badRate = datum.errorRate + (datum.refusedRate ?? 0);

        return (
          <g
            key={tile.key || "__empty__"}
            className={onSelect ? "cursor-pointer" : undefined}
            onClick={onSelect ? () => onSelect(tile.key) : undefined}
          >
            <title>
              {`${datum.label} - ${formatPercent(share)} - ${datum.count.toLocaleString()} requests - ${formatMs(datum.durationMsSum)} total`}
              {badRate > 0 ? ` - ${formatPercent(datum.errorRate)} errors` : ""}
            </title>
            <rect
              x={tile.x}
              y={tile.y}
              width={w}
              height={h}
              rx={3}
              fill={datum.color}
              className="transition-opacity hover:opacity-80"
            />
            {badRate > 0 && h > 8 && (
              // Failure share, on its own channel: a rule along the bottom
              // whose width is the fraction of requests that did not succeed.
              <rect
                x={tile.x}
                y={tile.y + h - 3}
                width={w * Math.min(1, badRate)}
                height={3}
                rx={1.5}
                className="fill-error"
              />
            )}
            {showLabel && (
              <text
                x={tile.x + 8}
                y={tile.y + 20}
                className="pointer-events-none fill-white text-[13px] font-medium [paint-order:stroke] [stroke-linejoin:round] [stroke-width:3px]"
                stroke="rgb(0 0 0 / 0.35)"
              >
                {datum.label}
              </text>
            )}
            {showValue && (
              <text
                x={tile.x + 8}
                y={tile.y + 38}
                className="pointer-events-none fill-white/85 font-mono text-[11px] [paint-order:stroke] [stroke-linejoin:round] [stroke-width:3px]"
                stroke="rgb(0 0 0 / 0.35)"
              >
                {formatPercent(share)}
              </text>
            )}
          </g>
        );
      })}
    </svg>
  );
}
