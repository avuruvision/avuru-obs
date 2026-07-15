"use client";

import { useMemo } from "react";
import { ChevronDown } from "lucide-react";
import { DropdownMenu } from "@/components/ui/dropdown-menu";
import { useURLState } from "@/hooks/use-url-state";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { isSpanError } from "@/lib/span-status";
import { cn } from "@/lib/cn";
import type { TraceResponse } from "@/lib/api-types";
import { CLEAR_WORKSPACE_PARAMS } from "./workspace-params";

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
// errors) plus the per-service color legend. Legend chips are the service
// perspective entry point: click toggles ?focus= (dim other services in the
// waterfall), the caret menu jumps to the service's participant-filtered
// trace list.
export function TraceSummaryBar({ trace }: { trace: TraceResponse }) {
  const { get, setMany } = useURLState();
  const services = useMemo(
    () => [...new Set(trace.spans.map((s) => s.service))],
    [trace],
  );
  const errorCount = useMemo(() => trace.spans.filter(isSpanError).length, [trace]);
  const focus = get("focus");

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
        {services.map((s) => {
          const focused = focus === s;
          return (
            <span
              key={s}
              className={cn(
                "inline-flex items-stretch overflow-hidden rounded-md bg-base-300/60 text-[10px]",
                focused && "bg-base-300 ring-1",
              )}
              style={focused ? ({ "--tw-ring-color": serviceColor(s) } as React.CSSProperties) : undefined}
            >
              <button
                type="button"
                aria-pressed={focused}
                title={focused ? "Clear focus" : `Focus ${s} spans in this trace`}
                onClick={() => setMany({ focus: focused ? undefined : s })}
                className="inline-flex items-center gap-1 px-1.5 py-0.5 hover:bg-base-300"
              >
                <span
                  className="h-2 w-2 rounded-sm"
                  style={{ backgroundColor: serviceColor(s) }}
                  aria-hidden
                />
                {s}
              </button>
              <DropdownMenu
                ariaLabel={`${s} actions`}
                align="start"
                triggerClassName="inline-flex items-center border-l border-base-100/60 px-0.5 text-base-content/50 hover:bg-base-300 hover:text-base-content"
                trigger={<ChevronDown className="h-3 w-3" aria-hidden />}
                items={[
                  {
                    label: "View service traces",
                    onSelect: () =>
                      setMany({ ...CLEAR_WORKSPACE_PARAMS, service: s, tab: "traces" }),
                  },
                ]}
              />
            </span>
          );
        })}
      </div>
    </div>
  );
}
