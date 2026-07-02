"use client";

// Small axis-less SVG line chart for one or more series over a shared time
// axis — no chart dependency (same policy as heatmap/sparklines). Labels show
// the latest value; the max is annotated top-left.
export interface ChartSeries {
  label: string;
  values: number[];
  className: string; // text-* color class; stroke uses currentColor
}

export function RedChart({
  title,
  series,
  format,
  height = 64,
}: {
  title: string;
  series: ChartSeries[];
  format: (v: number) => string;
  height?: number;
}) {
  const width = 240;
  const pad = 4;
  const max = Math.max(...series.flatMap((s) => s.values), 0);
  const n = Math.max(...series.map((s) => s.values.length));

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between">
        <span className="text-[11px] uppercase tracking-wide text-base-content/50">{title}</span>
        <span className="font-mono text-[11px] text-base-content/40">max {format(max)}</span>
      </div>
      <svg
        width="100%"
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        role="img"
        aria-label={title}
        className="rounded bg-base-300/40"
      >
        {series.map((s) => {
          if (s.values.length < 2) return null;
          const step = (width - pad * 2) / (n - 1 || 1);
          const coords = s.values
            .map((v, i) => {
              const x = pad + i * step;
              const y = pad + (1 - (max ? v / max : 0)) * (height - pad * 2);
              return `${x.toFixed(1)},${y.toFixed(1)}`;
            })
            .join(" ");
          return (
            <polyline
              key={s.label}
              points={coords}
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinejoin="round"
              className={s.className}
              vectorEffect="non-scaling-stroke"
            />
          );
        })}
      </svg>
      <div className="flex flex-wrap gap-x-3 gap-y-0.5">
        {series.map((s) => (
          <span key={s.label} className={`text-[11px] ${s.className}`}>
            ● {s.label}
            {s.values.length > 0 && (
              <span className="ml-1 font-mono text-base-content/60">
                {format(s.values[s.values.length - 1])}
              </span>
            )}
          </span>
        ))}
      </div>
    </div>
  );
}
