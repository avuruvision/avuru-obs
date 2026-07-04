"use client";

import { useMemo } from "react";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { isSpanError } from "@/lib/span-status";
import type { TraceResponse } from "@/lib/api-types";

function Stat({
  label,
  value,
  title,
  tone,
}: {
  label: string;
  value: string | number;
  title?: string;
  tone?: "error";
}) {
  return (
    <div className="flex flex-col" title={title}>
      <span className="text-[9px] font-semibold uppercase tracking-wider text-base-content/45">
        {label}
      </span>
      <span className={tone === "error" ? "font-mono text-xs font-semibold text-error" : "font-mono text-xs"}>
        {value}
      </span>
    </div>
  );
}

// SkyWalking-style trace stat block (started / duration / spans / services /
// errors) plus the per-service color legend.
export function TraceSummaryBar({ trace }: { trace: TraceResponse }) {
  const services = useMemo(
    () => [...new Set(trace.spans.map((s) => s.service))],
    [trace],
  );
  const errorCount = useMemo(() => trace.spans.filter(isSpanError).length, [trace]);

  return (
    <div
      role="group"
      aria-label="Trace summary"
      className="flex flex-wrap items-center gap-x-6 gap-y-2 border-b border-neutral/60 px-3 py-1.5"
    >
      <Stat label="Started" value={formatTime(trace.startTime)} title={utcTooltip(trace.startTime)} />
      <Stat label="Duration" value={formatMs(trace.durationMs)} />
      <Stat label="Spans" value={trace.spans.length} />
      <Stat label="Services" value={services.length} />
      {errorCount > 0 && <Stat label="Errors" value={errorCount} tone="error" />}
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        {services.map((s) => (
          <span
            key={s}
            className="inline-flex items-center gap-1 rounded-md bg-base-300/60 px-1.5 py-0.5 text-[10px]"
          >
            <span
              className="h-2 w-2 rounded-sm"
              style={{ backgroundColor: serviceColor(s) }}
              aria-hidden
            />
            {s}
          </span>
        ))}
      </div>
    </div>
  );
}
