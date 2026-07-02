"use client";

import type { MetricPoint } from "@/lib/api-types";

// Inline SVG sparkline — no chart dependency, same spirit as the CSS-grid
// heatmap. Scales to the series' own min/max so shape is visible at any unit.
export function Sparkline({
  points,
  width = 96,
  height = 24,
  className = "text-primary",
}: {
  points: MetricPoint[];
  width?: number;
  height?: number;
  className?: string;
}) {
  if (points.length < 2) {
    return <span className="text-xs text-base-content/30">—</span>;
  }
  const values = points.map((p) => p.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const pad = 2;
  const step = (width - pad * 2) / (points.length - 1);
  const coords = values
    .map((v, i) => {
      const x = pad + i * step;
      const y = pad + (1 - (v - min) / span) * (height - pad * 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={className}
      role="img"
      aria-label="usage trend"
    >
      <polyline
        points={coords}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
