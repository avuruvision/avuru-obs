"use client";

import { useMemo } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatMs } from "@/lib/format";
import { cn } from "@/lib/cn";
import { buildRows, serviceColor } from "@/lib/trace";
import type { Span, TraceResponse } from "@/lib/api-types";

// Quarter gridlines: enough to read positions without chart clutter.
const TICKS = [0, 0.25, 0.5, 0.75];

const GRID_COLS = "grid-cols-[minmax(180px,28%)_1fr_72px]";

// Gridlines drawn as a repeating 1px gradient across the bar track — the
// header ticks above use the same fractions, so bars are readable against
// absolute time (SkyWalking-style) without a chart dependency.
const gridStyle = {
  backgroundImage:
    "linear-gradient(90deg, color-mix(in oklab, var(--color-base-content) 12%, transparent) 1px, transparent 1px)",
  backgroundSize: "25% 100%",
};

// Span-tree waterfall. Selection is lifted to the parent so the detail renders
// in a side panel (split), not inline.
export function Waterfall({
  trace,
  selectedSpanId,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const rows = useMemo(() => buildRows(trace.spans), [trace.spans]);

  const t0 = new Date(trace.startTime).getTime();
  const total = Math.max(trace.durationMs, 0.001);

  return (
    <div className="flex flex-col">
      {/* Time axis over the bar column. */}
      <div className={cn("grid items-end gap-2 border-b border-neutral/50 pb-1 pr-2", GRID_COLS)}>
        <span />
        <span className="relative h-3 font-mono text-[10px] leading-3 text-base-content/45">
          {TICKS.map((f) => (
            <span key={f} className="absolute" style={{ left: `${f * 100}%` }}>
              {f === 0 ? "0" : formatMs(total * f)}
            </span>
          ))}
          <span className="absolute right-0">{formatMs(total)}</span>
        </span>
        <span />
      </div>
      {rows.map(({ span, depth }) => {
        const left = ((new Date(span.startTime).getTime() - t0) / total) * 100;
        const width = Math.max((span.durationMs / total) * 100, 0.4);
        const share = ((span.durationMs / total) * 100).toFixed(1);
        const isError = span.statusCode === "Error";
        const isSelected = selectedSpanId === span.spanId;
        return (
          <button
            key={span.spanId}
            onClick={() => onSelectSpan?.(span)}
            title={`${span.operation} — ${formatMs(span.durationMs)} (${share}% of trace)`}
            className={cn(
              "grid w-full items-center gap-2 border-b border-neutral/30 py-1 pr-2 text-left transition-colors hover:bg-base-300/40",
              GRID_COLS,
              isSelected && "bg-base-300/50",
            )}
          >
            <span
              className="flex min-w-0 items-center gap-1 text-xs"
              style={{ paddingLeft: `${depth * 14 + 8}px` }}
            >
              {isSelected ? (
                <ChevronDown className="h-3 w-3 shrink-0 text-base-content/40" />
              ) : (
                <ChevronRight className="h-3 w-3 shrink-0 text-base-content/40" />
              )}
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: serviceColor(span.service) }}
                aria-hidden
              />
              <span className="truncate font-medium">{span.service}</span>
              <span className="truncate font-mono text-base-content/55">{span.operation}</span>
            </span>
            <span className="relative h-4 rounded bg-base-300/40" style={gridStyle}>
              <span
                className={cn("absolute top-0 h-full rounded", isError && "bg-error")}
                style={{
                  left: `${left}%`,
                  width: `${width}%`,
                  backgroundColor: isError ? undefined : serviceColor(span.service),
                }}
              />
            </span>
            <span
              className={cn(
                "text-right font-mono text-xs tabular-nums",
                isError ? "font-semibold text-error" : "text-base-content/70",
              )}
            >
              {formatMs(span.durationMs)}
            </span>
          </button>
        );
      })}
    </div>
  );
}
