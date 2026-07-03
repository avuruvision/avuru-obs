"use client";

import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { childrenByParent, selfTimeMs, serviceColor } from "@/lib/trace";
import type { Span, TraceResponse } from "@/lib/api-types";

type SortKey = "service" | "operation" | "start" | "duration" | "self";

interface RowData {
  span: Span;
  startOffsetMs: number;
  selfMs: number;
}

// Numeric columns default to descending (slowest/latest first); text columns
// default to ascending.
const defaultDesc = (key: SortKey) =>
  key === "duration" || key === "self" || key === "start";

// Flat, sortable span list — Jaeger's "Trace Spans Table". Selection is lifted
// so the detail renders in the side panel (split).
export function SpansTable({
  trace,
  selectedSpanId,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({
    key: "start",
    desc: false,
  });

  const rows = useMemo<RowData[]>(() => {
    const t0 = new Date(trace.startTime).getTime();
    const byParent = childrenByParent(trace.spans);
    return trace.spans.map((span) => ({
      span,
      startOffsetMs: new Date(span.startTime).getTime() - t0,
      selfMs: selfTimeMs(span, byParent.get(span.spanId) ?? []),
    }));
  }, [trace]);

  const sorted = useMemo(() => {
    const dir = sort.desc ? -1 : 1;
    return [...rows].sort((a, b) => {
      switch (sort.key) {
        case "duration":
          return (a.span.durationMs - b.span.durationMs) * dir;
        case "self":
          return (a.selfMs - b.selfMs) * dir;
        case "service":
          return a.span.service.localeCompare(b.span.service) * dir;
        case "operation":
          return a.span.operation.localeCompare(b.span.operation) * dir;
        default:
          return (a.startOffsetMs - b.startOffsetMs) * dir;
      }
    });
  }, [rows, sort]);

  const onSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, desc: !s.desc } : { key, desc: defaultDesc(key) }));

  // A sortable header cell, rendered inline (not a nested component, which would
  // remount on every render).
  const th = (k: SortKey, label: string, right?: boolean) => (
    <th className={cn(right && "text-right")}>
      <button
        className="inline-flex items-center gap-1 hover:text-base-content"
        onClick={() => onSort(k)}
      >
        {label}
        {sort.key === k && <span className="text-[9px]">{sort.desc ? "▼" : "▲"}</span>}
      </button>
    </th>
  );

  return (
    <table className="table-dense w-full text-sm">
      <thead>
        <tr className="border-b border-neutral text-left text-base-content/60">
          {th("service", "Service")}
          {th("operation", "Operation")}
          <th>Kind</th>
          {th("start", "Start", true)}
          {th("duration", "Duration", true)}
          {th("self", "Self", true)}
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        {sorted.map(({ span, startOffsetMs, selfMs }) => {
          const isSelected = selectedSpanId === span.spanId;
          const isError = span.statusCode === "Error";
          return (
            <tr
              key={span.spanId}
              onClick={() => onSelectSpan?.(span)}
              className={cn(
                "cursor-pointer border-b border-neutral/40",
                isSelected ? "bg-base-300/50" : "hover:bg-base-300/40",
              )}
            >
              <td>
                <span className="flex items-center gap-1.5">
                  <span
                    className="h-2.5 w-2.5 shrink-0 rounded-sm"
                    style={{ backgroundColor: serviceColor(span.service) }}
                    aria-hidden
                  />
                  <span className="truncate font-medium">{span.service}</span>
                </span>
              </td>
              <td className="max-w-72 truncate font-mono text-xs">{span.operation}</td>
              <td className="text-xs text-base-content/60">{span.kind}</td>
              <td className="text-right font-mono text-xs text-base-content/60">
                {formatMs(startOffsetMs)}
              </td>
              <td
                className={cn(
                  "text-right font-mono text-xs",
                  isError && "font-semibold text-error",
                )}
              >
                {formatMs(span.durationMs)}
              </td>
              <td className="text-right font-mono text-xs text-base-content/60">
                {formatMs(selfMs)}
              </td>
              <td>
                <Badge tone={isError ? "error" : "neutral"}>{span.statusCode}</Badge>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
