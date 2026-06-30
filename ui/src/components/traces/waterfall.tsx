"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatMs } from "@/lib/format";
import { cn } from "@/lib/cn";
import { buildRows, serviceColor } from "@/lib/trace";
import { SpanDetail } from "./span-detail";
import type { TraceResponse } from "@/lib/api-types";

export function Waterfall({ trace }: { trace: TraceResponse }) {
  const [openSpanId, setOpenSpanId] = useState<string | null>(null);
  const rows = useMemo(() => buildRows(trace.spans), [trace.spans]);

  const t0 = new Date(trace.startTime).getTime();
  const total = Math.max(trace.durationMs, 0.001);

  return (
    <div className="flex flex-col">
      {rows.map(({ span, depth }) => {
        const left = ((new Date(span.startTime).getTime() - t0) / total) * 100;
        const width = Math.max((span.durationMs / total) * 100, 0.4);
        const isError = span.statusCode === "Error";
        const isOpen = openSpanId === span.spanId;
        return (
          <div key={span.spanId} className="group">
            <button
              onClick={() => setOpenSpanId(isOpen ? null : span.spanId)}
              className={cn(
                "grid w-full grid-cols-[minmax(180px,28%)_1fr_72px] items-center gap-2 border-b border-neutral/30 py-1 pr-2 text-left transition-colors hover:bg-base-300/40",
                isOpen && "bg-base-300/50",
              )}
            >
              <span
                className="flex min-w-0 items-center gap-1 text-xs"
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
              >
                {isOpen ? (
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
                <span className="truncate font-mono text-base-content/55">
                  {span.operation}
                </span>
              </span>
              <span className="relative h-4 rounded bg-base-300/40">
                <span
                  className={cn("absolute top-0 h-full rounded", isError ? "bg-error" : "")}
                  style={{
                    left: `${left}%`,
                    width: `${width}%`,
                    backgroundColor: isError ? undefined : serviceColor(span.service),
                  }}
                />
              </span>
              <span
                className={cn(
                  "text-right font-mono text-xs",
                  isError ? "font-semibold text-error" : "text-base-content/70",
                )}
              >
                {formatMs(span.durationMs)}
              </span>
            </button>
            {isOpen && <SpanDetail span={span} />}
          </div>
        );
      })}
    </div>
  );
}
