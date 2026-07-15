"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatMs } from "@/lib/format";
import { cn } from "@/lib/cn";
import { buildRows, serviceColor } from "@/lib/trace";
import { kindIcon, spanComponent, spanPeer } from "@/lib/component";
import { spanStatus } from "@/lib/span-status";
import type { Span, TraceResponse } from "@/lib/api-types";

// Quarter gridlines: enough to read positions without chart clutter.
const TICKS = [0, 0.25, 0.5, 0.75];

const GRID_COLS = "grid-cols-[minmax(240px,36%)_1fr_84px]";

// Gridlines drawn as a repeating 1px gradient across the bar track — the
// header ticks above use the same fractions, so bars are readable against
// absolute time (SkyWalking-style) without a chart dependency.
const gridStyle = {
  backgroundImage:
    "linear-gradient(90deg, color-mix(in oklab, var(--color-base-content) 12%, transparent) 1px, transparent 1px)",
  backgroundSize: "25% 100%",
};

const STATUS_DOT = {
  ok: "bg-success",
  error: "bg-error",
  unset: "bg-base-content/25",
} as const;

// Span-tree waterfall. Selection is lifted to the parent so the detail renders
// in a side panel (split), not inline. The chevron collapses a span's subtree
// (SkyWalking-style); each row carries a status dot, the entry/exit glyph, the
// detected component icon, and the remote peer for outgoing spans.
export function Waterfall({
  trace,
  selectedSpanId,
  focusService,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  focusService?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const rows = useMemo(() => buildRows(trace.spans, collapsed), [trace.spans, collapsed]);

  // A span selected at mount time is a deep link (span-id search, shared URL):
  // bring it into view once. Later clicks select rows already on screen.
  const deepLinkSpan = useRef(selectedSpanId);

  // Focusing a service (legend chip / ?focus= deep link) scrolls to its first
  // span. A span deep link is more specific, so it wins at mount: seeding
  // prevFocus with the current focus makes the effect see "no change".
  const firstFocusSpanId = useMemo(
    () => (focusService ? (rows.find((r) => r.span.service === focusService)?.span.spanId ?? null) : null),
    [rows, focusService],
  );
  const firstFocusRow = useRef<HTMLButtonElement | null>(null);
  // Initializer only matters on the first render, where selectedSpanId IS the
  // span deep link (same seed as deepLinkSpan above).
  const prevFocus = useRef<string | null>(selectedSpanId ? (focusService ?? null) : null);
  useEffect(() => {
    if (focusService && focusService !== prevFocus.current) {
      firstFocusRow.current?.scrollIntoView({ block: "center" });
    }
    prevFocus.current = focusService ?? null;
  }, [focusService]);

  const toggle = (spanId: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (!next.delete(spanId)) next.add(spanId);
      return next;
    });

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
      {rows.map(({ span, depth, hasChildren, descendantCount }) => {
        const left = ((new Date(span.startTime).getTime() - t0) / total) * 100;
        const width = Math.max((span.durationMs / total) * 100, 0.4);
        const share = ((span.durationMs / total) * 100).toFixed(1);
        const status = spanStatus(span);
        const isError = status.kind === "error";
        const isSelected = selectedSpanId === span.spanId;
        const isCollapsed = collapsed.has(span.spanId);
        const component = spanComponent(span);
        const peer = spanPeer(span);
        const KindIcon = kindIcon(span.kind);
        const isDimmed = Boolean(focusService) && span.service !== focusService;
        return (
          <button
            key={span.spanId}
            ref={(el) => {
              if (el && span.spanId === deepLinkSpan.current) {
                deepLinkSpan.current = null;
                el.scrollIntoView({ block: "center" });
              }
              if (el && span.spanId === firstFocusSpanId) firstFocusRow.current = el;
            }}
            onClick={() => onSelectSpan?.(span)}
            title={`${span.operation} — ${formatMs(span.durationMs)} (${share}% of trace)`}
            className={cn(
              "grid w-full items-center gap-2 border-b border-neutral/30 py-0.5 pr-2 text-left transition-colors hover:bg-base-300/40",
              GRID_COLS,
              isSelected && "bg-base-300/50",
              isDimmed && "opacity-40",
            )}
          >
            <span className="flex min-w-0 items-center gap-1 pl-2 text-xs">
              {Array.from({ length: depth }, (_, i) => (
                <span
                  key={i}
                  className="w-2.5 shrink-0 self-stretch border-l border-base-content/10"
                  aria-hidden
                />
              ))}
              {hasChildren ? (
                <span
                  role="button"
                  tabIndex={0}
                  aria-label={isCollapsed ? "Expand subtree" : "Collapse subtree"}
                  onClick={(e) => {
                    e.stopPropagation();
                    toggle(span.spanId);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      e.stopPropagation();
                      toggle(span.spanId);
                    }
                  }}
                  className="shrink-0 rounded text-base-content/40 hover:text-base-content"
                >
                  {isCollapsed ? (
                    <ChevronRight className="h-3 w-3" />
                  ) : (
                    <ChevronDown className="h-3 w-3" />
                  )}
                </span>
              ) : (
                <span className="w-3 shrink-0" aria-hidden />
              )}
              <span
                className={cn("h-2 w-2 shrink-0 rounded-full", STATUS_DOT[status.kind])}
                title={`status: ${status.label}`}
              />
              {KindIcon && (
                <span className="shrink-0" title={span.kind}>
                  <KindIcon className="h-3 w-3 text-base-content/40" />
                </span>
              )}
              <span className="shrink-0" title={component.name}>
                <component.Icon className="h-3 w-3 text-base-content/50" />
              </span>
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: serviceColor(span.service) }}
                aria-hidden
              />
              <span className="truncate font-medium">{span.service}</span>
              <span className="truncate font-mono text-base-content/55">{span.operation}</span>
              {peer && (
                <span className="truncate text-[10px] text-base-content/40">→ {peer}</span>
              )}
              {isCollapsed && descendantCount > 0 && (
                <span className="shrink-0 rounded bg-base-300 px-1 text-[10px] text-base-content/50">
                  +{descendantCount}
                </span>
              )}
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
