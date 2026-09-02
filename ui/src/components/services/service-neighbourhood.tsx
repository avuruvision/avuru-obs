"use client";

import { useEffect, useMemo, useRef } from "react";
import { Maximize, Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { formatBytes, formatMs, formatPercent, formatRate } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { statusDotClass } from "@/lib/health-status";
import { usePanZoom } from "@/hooks/use-pan-zoom";
import { virtualLabel } from "@/components/service-map/graph-elements";
import {
  buildNeighbourhood,
  CARD_H,
  CARD_W,
  type PlacedLink,
  type PlacedNode,
} from "@/lib/neighbourhood-layout";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

// A service's neighbourhood as a picture: callers on the left, this service in
// the middle, what it depends on on the right.
//
// Distinct from the service map, which draws the whole estate and lays it out
// by simulation. Here the columns ARE the meaning — left calls right — so the
// layout is fixed, and the reader can see in one glance which caller carries
// the traffic and which dependency is the one going red. The table beside it
// stays the complete, precise list; this answers the shape.
export function ServiceNeighbourhood({
  service,
  services,
  upstream,
  downstream,
  windowMs,
  health,
  healthEnabled,
  onSelect,
}: {
  service: string;
  services: ServiceStats[];
  upstream: ServiceEdge[];
  downstream: ServiceEdge[];
  windowMs: number;
  health: Map<string, ServiceHealth>;
  healthEnabled: boolean;
  onSelect: (service: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const { nodes, links, width, height } = useMemo(
    () => buildNeighbourhood({ service, services, upstream, downstream }),
    [service, services, upstream, downstream],
  );

  const { transform, handlers, zoomIn, zoomOut, fit } = usePanZoom(containerRef);

  // Refit whenever the graph changes size — a different service, or a window
  // with more neighbours in it, is a different picture.
  useEffect(() => {
    fit(width, height);
  }, [fit, width, height]);

  return (
    <div
      ref={containerRef}
      {...handlers}
      data-testid="service-neighbourhood"
      className="relative h-[42vh] min-h-75 cursor-grab touch-none overflow-hidden rounded-lg border border-neutral bg-base-200"
    >
      <div
        className="absolute left-0 top-0 origin-top-left will-change-transform"
        style={{
          transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.k})`,
          width,
          height,
        }}
      >
        <svg width={width} height={height} className="absolute left-0 top-0 overflow-visible">
          <defs>
            {/* Its own id: SVG ids are document-global, and the trace path view
                already owns `path-arrow`. */}
            <marker
              id="neighbourhood-arrow"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M0,0 L8,4 L0,8 z" fill="var(--color-neutral)" />
            </marker>
          </defs>
          {links.map((link) => (
            <Link key={link.key} link={link} windowMs={windowMs} />
          ))}
        </svg>

        {nodes.map((node) => (
          <NodeCard
            key={node.id}
            node={node}
            status={healthEnabled ? health.get(node.name)?.status : undefined}
            onSelect={() => onSelect(node.name)}
          />
        ))}
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
          onClick={() => fit(width, height)}
        >
          <Maximize className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

function Link({ link, windowMs }: { link: PlacedLink; windowMs: number }) {
  const { edge, from, to } = link;
  const x1 = from.x + CARD_W;
  const y1 = from.y + CARD_H / 2;
  const x2 = to.x;
  const y2 = to.y + CARD_H / 2;
  const mid = (x1 + x2) / 2;
  const bad = edge.errorCount > 0;
  // A connection the kernel saw and nobody traced. It carries bytes and zero
  // calls by construction, so "0.0/min" would read as "this path is idle" when
  // the truth is that nothing measured it.
  const flowOnly = edge.calls === 0;
  // The curve's own midpoint, which for this symmetric bezier is the middle of
  // the column gap: the one anchor that clears the card at both ends however
  // long the label gets.
  const lx = mid;
  const ly = (y1 + y2) / 2;
  const via = edge.viaTransport?.length ? edge.viaTransport.join(", ") : undefined;

  return (
    <g>
      <path
        d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`}
        fill="none"
        stroke={bad ? "var(--color-error)" : "var(--color-neutral)"}
        strokeWidth={1.5}
        // Dashed when the far end never confirmed any of this — same encoding
        // the map gives a flow-derived edge.
        strokeDasharray={flowOnly ? "4 3" : undefined}
        markerEnd="url(#neighbourhood-arrow)"
      />
      <text
        x={lx}
        y={ly - (via ? 12 : 6)}
        textAnchor="middle"
        className="fill-base-content/50 font-mono text-[10px]"
      >
        {flowOnly
          ? edge.bytes
            ? formatBytes(edge.bytes)
            : "network flow"
          : formatRate(edge.calls / (windowMs / 1000))}
        {/* Absent p95 means "not measured" — a flow-derived edge has no span to
            time — and must never be drawn as 0ms. */}
        {edge.p95Ms !== undefined && ` · ${formatMs(edge.p95Ms)}`}
        {bad && <tspan className="fill-error"> · {formatPercent(edge.errorRate)}</tspan>}
      </text>
      {via && (
        // The dependency is real, but the hub had to walk the trace's parent
        // chain across a proxy to see it. Saying so is the difference between a
        // fact and a guess.
        <text
          x={lx}
          y={ly - 1}
          textAnchor="middle"
          className="fill-base-content/35 font-mono text-[9px]"
        >
          via {via}
        </text>
      )}
    </g>
  );
}

function NodeCard({
  node,
  status,
  onSelect,
}: {
  node: PlacedNode;
  status?: string;
  onSelect: () => void;
}) {
  const { stats } = node;
  // A derived dependency, an unresolved peer, the overflow marker and the
  // "nothing here" placeholder are all outlined rather than filled: none of
  // them is a workload that reported anything, and drawing one solid would
  // claim knowledge nobody has.
  const solid = node.kind === "centre" || node.kind === "service";
  const Tag = node.clickable ? "button" : "div";

  return (
    <Tag
      onClick={node.clickable ? onSelect : undefined}
      title={node.title}
      style={{ left: node.x, top: node.y, width: CARD_W, height: CARD_H }}
      className={cn(
        "absolute flex flex-col justify-center gap-1 rounded-lg border px-2 text-left transition-colors",
        solid ? "bg-base-100" : "border-dashed bg-base-100/50",
        node.kind === "centre" ? "border-primary/60 ring-1 ring-primary/40" : "border-neutral",
        node.clickable && "cursor-pointer hover:border-primary/60",
      )}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <span
          className={cn("h-2.5 w-2.5 shrink-0 rounded-sm", !solid && "border border-base-content/30")}
          style={solid ? { backgroundColor: serviceColor(node.name) } : undefined}
          aria-hidden
        />
        <span
          className={cn(
            "truncate text-xs font-medium",
            solid ? "font-mono" : "font-mono text-base-content/70",
            node.kind === "empty" || node.kind === "more" ? "font-sans text-base-content/50" : "",
          )}
        >
          {node.kind === "derived" ? virtualLabel(node.name) : node.name}
        </span>
        {status && (
          <span
            className={cn("ml-auto h-2 w-2 shrink-0 rounded-full", statusDotClass(status))}
            title={`health: ${status}`}
            aria-hidden
          />
        )}
      </span>

      {stats && node.kind !== "derived" ? (
        <span className="flex items-center gap-1.5 text-[10px] text-base-content/55">
          <span className="font-mono">{formatRate(stats.ratePerSec)}</span>
          <span className="text-base-content/30">·</span>
          <span className="font-mono">p95 {formatMs(stats.p95Ms)}</span>
          {stats.errorRate > 0 && (
            <span className="ml-auto font-mono text-error">{formatPercent(stats.errorRate)}</span>
          )}
        </span>
      ) : (
        <span
          className={cn(
            "text-[10px] leading-tight text-base-content/40",
            // A placeholder's note is the teaching, so it wraps rather than
            // truncating; a real node's is short by construction.
            node.kind === "derived" ? "truncate" : "",
          )}
        >
          {node.note}
        </span>
      )}

      {stats?.role === "transport" && (
        <span className="truncate text-[9px] uppercase tracking-wide text-base-content/30">
          mesh · carries other services&rsquo; traffic
        </span>
      )}
    </Tag>
  );
}
