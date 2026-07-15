"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Maximize, Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { spanComponent } from "@/lib/component";
import { spanStatus } from "@/lib/span-status";
import { usePanZoom } from "@/hooks/use-pan-zoom";
import {
  CARD_H,
  CARD_W,
  initialCollapsed,
  layoutTraceTree,
  type TreeNode,
} from "@/lib/trace-tree-layout";
import type { Span, TraceResponse } from "@/lib/api-types";

const STATUS_DOT = {
  ok: "bg-success",
  error: "bg-error",
  unset: "bg-base-content/25",
} as const;

const LARGE_TRACE = 400;

// SkyWalking-style call tree: one rich card per span, laid out left-to-right,
// with pan/zoom and per-subtree collapse. Clicking a card selects the span
// (same drawer as the other views).
export function TraceTree({
  trace,
  selectedSpanId,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() =>
    initialCollapsed(trace.spans),
  );
  const [expandedStubs, setExpandedStubs] = useState<ReadonlySet<string>>(new Set());

  const layout = useMemo(
    () => layoutTraceTree(trace.spans, { collapsed, expandedStubs }),
    [trace.spans, collapsed, expandedStubs],
  );
  const { transform, handlers, zoomIn, zoomOut, fit } = usePanZoom(containerRef);

  // The parent remounts this component per trace (key={traceId}), so state
  // resets for free; fit once on mount, not on every collapse relayout.
  useEffect(() => {
    fit(layout.width, layout.height);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fit]);

  const toggle = (spanId: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (!next.delete(spanId)) next.add(spanId);
      return next;
    });

  return (
    <div
      ref={containerRef}
      {...handlers}
      className="relative h-[62vh] cursor-grab touch-none overflow-hidden rounded-lg border border-neutral bg-base-200"
    >
      <div
        className="absolute left-0 top-0 origin-top-left will-change-transform"
        style={{
          transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.k})`,
          width: layout.width,
          height: layout.height,
        }}
      >
        <svg
          width={layout.width}
          height={layout.height}
          className="absolute left-0 top-0 overflow-visible"
          aria-hidden
        >
          {layout.edges.map((e) => (
            <path
              key={`${e.from} ${e.to}`}
              d={e.path}
              fill="none"
              stroke="var(--color-neutral)"
              strokeWidth={1.5}
            />
          ))}
        </svg>
        {layout.nodes.map((n) =>
          n.type === "span" ? (
            <SpanCard
              key={n.key}
              node={n}
              selected={selectedSpanId === n.span.spanId}
              onSelect={() => onSelectSpan?.(n.span)}
              onToggle={() => toggle(n.span.spanId)}
            />
          ) : (
            <button
              key={n.key}
              onClick={() =>
                setExpandedStubs((prev) => new Set(prev).add(n.key))
              }
              className="absolute rounded-lg border border-dashed border-neutral bg-base-100/60 px-2 text-xs text-base-content/60 hover:border-base-content/40 hover:text-base-content"
              style={{ left: n.x, top: n.y, width: CARD_W, height: CARD_H }}
              title={`${n.count} more ${n.operation} — click to expand`}
            >
              ×{n.count} same: <span className="font-mono">{n.operation}</span>
            </button>
          ),
        )}
      </div>

      <div className="absolute right-2 top-2 flex gap-1 rounded-lg border border-neutral bg-base-100/90 p-0.5">
        <Button variant="ghost" size="icon" aria-label="Zoom in" onClick={zoomIn}>
          <Plus className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Zoom out" onClick={zoomOut}>
          <Minus className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Fit to view"
          onClick={() => fit(layout.width, layout.height)}
        >
          <Maximize className="h-4 w-4" />
        </Button>
      </div>

      {trace.spans.length > LARGE_TRACE && (
        <div className="absolute left-2 top-2 rounded-md bg-base-100/90 px-2 py-1 text-[10px] text-base-content/60">
          large trace — subtrees collapsed
        </div>
      )}
    </div>
  );
}

function SpanCard({
  node,
  selected,
  onSelect,
  onToggle,
}: {
  node: Extract<TreeNode, { type: "span" }>;
  selected: boolean;
  onSelect: () => void;
  onToggle: () => void;
}) {
  const { span } = node;
  const status = spanStatus(span);
  const component = spanComponent(span);
  return (
    <button
      onClick={onSelect}
      className={cn(
        "absolute flex flex-col justify-center gap-0.5 rounded-lg border bg-base-100 px-2 text-left shadow-sm transition-colors",
        status.kind === "error" ? "border-error/60" : "border-neutral",
        selected ? "ring-1 ring-primary" : "hover:border-base-content/40",
      )}
      style={{ left: node.x, top: node.y, width: CARD_W, height: CARD_H }}
    >
      <span className="flex w-full min-w-0 items-center gap-1">
        <span
          className={cn("h-2 w-2 shrink-0 rounded-full", STATUS_DOT[status.kind])}
          title={`status: ${status.label}`}
        />
        <span className="shrink-0" title={component.name}>
          <component.Icon className="h-3 w-3 text-base-content/50" />
        </span>
        <span className="min-w-0 truncate font-mono text-xs" title={span.operation}>
          {span.operation}
        </span>
        {node.hasChildren && (
          <span
            role="button"
            tabIndex={0}
            aria-label={node.collapsed ? "Expand subtree" : "Collapse subtree"}
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                e.stopPropagation();
                onToggle();
              }
            }}
            className="ml-auto shrink-0 rounded bg-base-300 px-1 font-mono text-[10px] text-base-content/60 hover:text-base-content"
          >
            {node.collapsed ? `+${node.hiddenDescendants}` : "−"}
          </span>
        )}
      </span>
      <span className="flex w-full min-w-0 items-center gap-1 text-[10px]">
        <span
          className="h-2 w-2 shrink-0 rounded-sm"
          style={{ backgroundColor: serviceColor(span.service) }}
          aria-hidden
        />
        <span className="min-w-0 truncate text-base-content/60">{span.service}</span>
        <span className="ml-auto shrink-0 font-mono tabular-nums text-base-content/70">
          {formatMs(span.durationMs)}
        </span>
      </span>
    </button>
  );
}
