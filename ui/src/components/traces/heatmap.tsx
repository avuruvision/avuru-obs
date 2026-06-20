"use client";

import { useMemo } from "react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatMs } from "@/lib/format";
import type { HeatmapResponse } from "@/lib/api-types";

// Latency × time heatmap, Coroot-style: gold intensity = volume, red tint =
// errors in the cell. CSS grid — no chart lib in M1. Click a cell to filter
// the trace list to that duration band.
export function Heatmap({
  data,
  isLoading,
  onSelectBand,
}: {
  data?: HeatmapResponse;
  isLoading: boolean;
  onSelectBand: (minMs: number, maxMs: number) => void;
}) {
  const grid = useMemo(() => {
    if (!data) return null;
    const cols = Math.max(...data.cells.map((c) => c.t), 59) + 1;
    const rows = data.durationBoundsMs.length;
    const maxCount = Math.max(...data.cells.map((c) => c.count), 1);
    const byPos = new Map(data.cells.map((c) => [`${c.t}:${c.d}`, c]));
    return { cols, rows, maxCount, byPos };
  }, [data]);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Latency & errors heatmap</CardTitle>
        {data && (
          <span className="text-xs text-base-content/50">
            {data.timeBucketSec}s buckets · click a cell to filter by duration
          </span>
        )}
      </CardHeader>
      <div className="px-4 pb-4">
        {isLoading || !grid || !data ? (
          <CenteredSpinner />
        ) : (
          <div className="flex gap-2">
            {/* duration axis */}
            <div
              className="grid shrink-0 text-right font-mono text-[9px] text-base-content/40"
              style={{ gridTemplateRows: `repeat(${grid.rows}, 10px)` }}
            >
              {data.durationBoundsMs.map((b, i) =>
                i % 4 === 3 ? (
                  <span key={i} style={{ gridRow: grid.rows - i }}>
                    {formatMs(b)}
                  </span>
                ) : null,
              )}
            </div>
            <div
              role="grid"
              aria-label="Latency heatmap"
              className="grid flex-1 gap-px"
              style={{
                gridTemplateColumns: `repeat(${grid.cols}, minmax(0,1fr))`,
                gridTemplateRows: `repeat(${grid.rows}, 10px)`,
              }}
            >
              {data.cells.map((c) => {
                const intensity = Math.sqrt(c.count / grid.maxCount);
                const hasErrors = c.errorCount > 0;
                const minMs = c.d === 0 ? 0 : data.durationBoundsMs[c.d - 1];
                const maxMs = data.durationBoundsMs[c.d];
                return (
                  <button
                    key={`${c.t}:${c.d}`}
                    role="gridcell"
                    title={`${c.count} traces${hasErrors ? `, ${c.errorCount} errors` : ""} · ${formatMs(minMs)}–${formatMs(maxMs)}`}
                    onClick={() => onSelectBand(minMs, maxMs)}
                    className="rounded-[1px] transition-transform hover:scale-125"
                    style={{
                      gridColumn: c.t + 1,
                      gridRow: grid.rows - c.d,
                      backgroundColor: hasErrors
                        ? `color-mix(in oklab, var(--color-error) ${30 + intensity * 70}%, transparent)`
                        : `color-mix(in oklab, var(--color-primary) ${15 + intensity * 85}%, transparent)`,
                    }}
                  />
                );
              })}
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}
