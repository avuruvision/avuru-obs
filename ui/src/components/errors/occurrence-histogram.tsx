import type { ErrorHistogramPoint } from "@/lib/api-types";

// A compact bar chart of occurrences over the selected window. Deliberately
// self-contained (no chart lib) — it's a sparkline-scale view, not the RED
// dashboard.
export function OccurrenceHistogram({ points }: { points: ErrorHistogramPoint[] }) {
  if (points.length === 0) {
    return <p className="text-xs text-base-content/50">No occurrences in this window.</p>;
  }
  const max = Math.max(...points.map((p) => p.count), 1);
  return (
    <div
      className="flex h-16 items-end gap-px"
      role="img"
      aria-label={`Occurrence histogram, ${points.length} buckets, peak ${max}`}
    >
      {points.map((p, i) => (
        <div
          key={i}
          className="flex-1 rounded-sm bg-error/60"
          style={{ height: `${Math.max((p.count / max) * 100, p.count > 0 ? 6 : 0)}%` }}
          title={`${new Date(p.time).toLocaleString()}: ${p.count}`}
        />
      ))}
    </div>
  );
}
