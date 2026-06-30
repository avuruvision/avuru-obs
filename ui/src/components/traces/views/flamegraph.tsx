"use client";

import { useMemo } from "react";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { buildRows, serviceColor } from "@/lib/trace";
import type { Span, TraceResponse } from "@/lib/api-types";

const ROW_H = 22;

// Icicle/flame chart of the span tree — Jaeger's "Trace Flamegraph". Rows are
// depth, x is start offset, width is duration (all relative to the trace).
// Selection is lifted so the detail renders in the side panel (split).
export function Flamegraph({
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
  const maxDepth = rows.reduce((m, r) => Math.max(m, r.depth), 0);

  return (
    <div className="relative w-full" style={{ height: (maxDepth + 1) * ROW_H }}>
      {rows.map(({ span, depth }) => {
        const left = ((new Date(span.startTime).getTime() - t0) / total) * 100;
        const width = Math.max((span.durationMs / total) * 100, 0.2);
        const isError = span.statusCode === "Error";
        const isSelected = selectedSpanId === span.spanId;
        return (
          <button
            key={span.spanId}
            title={`${span.service} · ${span.operation} · ${formatMs(span.durationMs)}`}
            onClick={() => onSelectSpan?.(span)}
            className={cn(
              "absolute overflow-hidden rounded-sm border border-base-100/30 px-1 text-left text-[10px] leading-[18px] text-black/75",
              isSelected && "ring-1 ring-base-content",
            )}
            style={{
              left: `${left}%`,
              width: `${width}%`,
              top: depth * ROW_H,
              height: ROW_H - 2,
              backgroundColor: isError ? "var(--color-error)" : serviceColor(span.service),
            }}
          >
            <span className="truncate">{span.operation}</span>
          </button>
        );
      })}
    </div>
  );
}
