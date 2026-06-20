"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatMs } from "@/lib/format";
import { cn } from "@/lib/cn";
import { SpanDetail } from "./span-detail";
import type { Span, TraceResponse } from "@/lib/api-types";

interface Row {
  span: Span;
  depth: number;
}

// Stable service hue from a name hash (consistent colors across screens).
function serviceHue(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360;
  return h;
}

function buildRows(spans: Span[]): Row[] {
  const byParent = new Map<string, Span[]>();
  const ids = new Set(spans.map((s) => s.spanId));
  for (const s of spans) {
    // Treat spans with missing parents as roots (partial traces happen).
    const parent = ids.has(s.parentSpanId) ? s.parentSpanId : "";
    const list = byParent.get(parent) ?? [];
    list.push(s);
    byParent.set(parent, list);
  }
  for (const list of byParent.values()) {
    list.sort((a, b) => a.startTime.localeCompare(b.startTime));
  }
  const rows: Row[] = [];
  const walk = (parent: string, depth: number) => {
    for (const s of byParent.get(parent) ?? []) {
      rows.push({ span: s, depth });
      walk(s.spanId, depth + 1);
    }
  };
  walk("", 0);
  return rows;
}

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
                  style={{ backgroundColor: `oklch(0.65 0.13 ${serviceHue(span.service)})` }}
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
                    backgroundColor: isError
                      ? undefined
                      : `oklch(0.65 0.13 ${serviceHue(span.service)})`,
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
