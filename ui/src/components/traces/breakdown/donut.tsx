"use client";

import { useMemo } from "react";
import { formatPercent } from "@/lib/format";
import type { TreemapDatum } from "./treemap";

const SIZE = 180;
const RADIUS = 70;
const STROKE = 26;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;
// Surface gap between adjacent segments, in path units, so two neighbouring
// slices of similar hue never read as one.
const GAP = 2;

// Donut of the same breakdown the treemap draws, for the part-of-whole reading
// a treemap is bad at: a handful of large shares, compared against each other.
//
// Segments are stroked arcs on one circle rather than filled wedges - the arc
// maths reduces to a dash pattern, which is exact at any size and needs no path
// generator.
export function Donut({
  data,
  weight,
  onSelect,
}: {
  data: TreemapDatum[];
  weight: "count" | "time";
  onSelect?: (key: string) => void;
}) {
  const { segments, total } = useMemo(() => {
    const value = (d: TreemapDatum) => (weight === "count" ? d.count : d.durationMsSum);
    const sum = data.reduce((acc, d) => acc + value(d), 0);
    // Arcs are cumulative, so this walks the list rather than mapping it: each
    // segment starts where the previous one ended.
    const segs: { datum: TreemapDatum; length: number; offset: number }[] = [];
    let offset = 0;
    for (const d of data) {
      const v = value(d);
      if (v <= 0) continue;
      const length = (sum > 0 ? v / sum : 0) * CIRCUMFERENCE;
      segs.push({ datum: d, length, offset });
      offset += length;
    }
    return { segments: segs, total: sum };
  }, [data, weight]);

  if (!segments.length || total <= 0) return null;

  return (
    <svg
      viewBox={`0 0 ${SIZE} ${SIZE}`}
      className="h-[180px] w-[180px] shrink-0"
      role="img"
      aria-label="Traffic breakdown donut"
    >
      {/* -90deg so the first slice starts at twelve o'clock, where a reader
          expects a part-of-whole chart to begin. */}
      <g transform={`rotate(-90 ${SIZE / 2} ${SIZE / 2})`}>
        {segments.map(({ datum, length, offset }) => (
          <circle
            key={datum.key || "__empty__"}
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={RADIUS}
            fill="none"
            stroke={datum.color}
            strokeWidth={STROKE}
            strokeDasharray={`${Math.max(0, length - GAP)} ${CIRCUMFERENCE - Math.max(0, length - GAP)}`}
            strokeDashoffset={-offset}
            className={onSelect ? "cursor-pointer transition-opacity hover:opacity-80" : undefined}
            onClick={onSelect ? () => onSelect(datum.key) : undefined}
          >
            <title>{`${datum.label} - ${formatPercent(length / CIRCUMFERENCE)}`}</title>
          </circle>
        ))}
      </g>
      <text
        x={SIZE / 2}
        y={SIZE / 2 - 2}
        textAnchor="middle"
        className="fill-base-content font-mono text-[15px] font-semibold"
      >
        {segments.length}
      </text>
      <text
        x={SIZE / 2}
        y={SIZE / 2 + 14}
        textAnchor="middle"
        className="fill-base-content/50 text-[10px] uppercase tracking-wide"
      >
        shown
      </text>
    </svg>
  );
}
