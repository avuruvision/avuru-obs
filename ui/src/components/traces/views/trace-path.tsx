"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Maximize, Minus, Plus, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { usePanZoom } from "@/hooks/use-pan-zoom";
import { buildTracePath, descendantsOf, type PathExit, type PathNode } from "@/lib/trace-path";
import type { Span, TraceResponse } from "@/lib/api-types";

const CARD_W = 190;
const CARD_H = 66;
const COL_GAP = 110; // horizontal space between hops, room for the edge label
const ROW_GAP = 22;
const PAD = 28;

interface Placed extends PathNode {
  x: number;
  y: number;
}

interface PlacedExit extends PathExit {
  x: number;
  y: number;
}

// The path one request took, at service granularity: which services it touched,
// in what order, and where its time went.
//
// Distinct from both neighbours in this panel. The Tree is per SPAN — accurate,
// and unreadable at 300 of them. The service map is per ESTATE — it aggregates
// the whole window and cannot describe a single request. This is the shape of
// THIS one.
export function TracePath({
  trace,
  selectedSpanId,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  // Focus is "show me only what this service caused" — the graph answer to
  // filtering a trace by a parent.
  const [focus, setFocus] = useState<string | null>(null);

  const path = useMemo(
    // `services` is the hub's per-service rollup, carried on this same
    // response. `?? []` is a guard against a partial object, not a fallback:
    // the browser no longer computes these numbers, so an empty list shows
    // zeros rather than quietly recomputing a second answer.
    () => buildTracePath(trace.spans, trace.services ?? []),
    [trace.spans, trace.services],
  );

  const visible = useMemo(
    () => (focus ? descendantsOf(path, focus) : null),
    [path, focus],
  );

  const { nodes, exits, edges, exitEdges, width, height, maxSelf } = useMemo(() => {
    const shown = path.nodes.filter((n) => !visible || visible.has(n.service));
    const shownServices = new Set(shown.map((n) => n.service));
    const shownExits = path.exits.filter((e) => shownServices.has(e.from));
    const depthOfService = new Map(shown.map((n) => [n.service, n.depth]));

    // Columns are service hops from the entry point, so the graph reads
    // left-to-right in the order the request travelled. An exit sits one hop
    // past whoever called it: it IS the next hop, just one nobody instrumented.
    type Cell = { kind: "service"; node: PathNode } | { kind: "exit"; exit: PathExit };
    const columns = new Map<number, Cell[]>();
    const push = (depth: number, cell: Cell) => {
      const list = columns.get(depth) ?? [];
      list.push(cell);
      columns.set(depth, list);
    };
    for (const n of shown) push(n.depth, { kind: "service", node: n });
    for (const e of shownExits) push((depthOfService.get(e.from) ?? 0) + 1, { kind: "exit", exit: e });

    const depths = [...columns.keys()].sort((a, b) => a - b);
    const tallest = Math.max(...depths.map((d) => columns.get(d)?.length ?? 0), 1);
    const placed: Placed[] = [];
    const placedExits: PlacedExit[] = [];
    depths.forEach((depth, col) => {
      const list = columns.get(depth) ?? [];
      // Each column is centred against the tallest one, so a path with a single
      // wide fan-out does not sit against the top edge.
      const offset = ((tallest - list.length) * (CARD_H + ROW_GAP)) / 2;
      const x = PAD + col * (CARD_W + COL_GAP);
      list.forEach((cell, row) => {
        const y = PAD + offset + row * (CARD_H + ROW_GAP);
        if (cell.kind === "service") placed.push({ ...cell.node, x, y });
        else placedExits.push({ ...cell.exit, x, y });
      });
    });

    const byService = new Map(placed.map((p) => [p.service, p]));
    const drawn = path.edges.filter(
      (e) => byService.has(e.source) && byService.has(e.target),
    );
    return {
      nodes: placed,
      exits: placedExits,
      edges: drawn.map((e) => ({
        ...e,
        from: byService.get(e.source) as Placed,
        to: byService.get(e.target) as Placed,
      })),
      exitEdges: placedExits
        .filter((e) => byService.has(e.from))
        .map((e) => ({ exit: e, from: byService.get(e.from) as Placed })),
      width: PAD * 2 + Math.max(depths.length, 1) * (CARD_W + COL_GAP) - COL_GAP,
      height: PAD * 2 + tallest * (CARD_H + ROW_GAP) - ROW_GAP,
      maxSelf: Math.max(...placed.map((p) => p.selfMs), 1),
    };
  }, [path, visible]);

  const { transform, handlers, zoomIn, zoomOut, fit } = usePanZoom(containerRef);

  // Refit when the visible set changes: focusing a subtree makes the graph a
  // different size, and leaving the old transform would strand it off-screen.
  useEffect(() => {
    fit(width, height);
  }, [fit, width, height]);

  const selectFirstSpan = (service: string) => {
    const node = path.nodes.find((n) => n.service === service);
    const span = trace.spans.find((s) => s.spanId === node?.firstSpanId);
    if (span) onSelectSpan?.(span);
  };

  return (
    <div
      ref={containerRef}
      {...handlers}
      data-testid="trace-path"
      className="relative h-[62vh] cursor-grab touch-none overflow-hidden rounded-lg border border-neutral bg-base-200"
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
            <marker
              id="path-arrow"
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
          {edges.map((e) => {
            const x1 = e.from.x + CARD_W;
            const y1 = e.from.y + CARD_H / 2;
            const x2 = e.to.x;
            const y2 = e.to.y + CARD_H / 2;
            const mid = (x1 + x2) / 2;
            return (
              <g key={`${e.source} ${e.target}`}>
                <path
                  d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`}
                  fill="none"
                  stroke={e.errorCount > 0 ? "var(--color-error)" : "var(--color-neutral)"}
                  strokeWidth={1.5}
                  markerEnd="url(#path-arrow)"
                />
                <text
                  // Anchored a third of the way along rather than at the
                  // midpoint: two edges crossing the same gap have different
                  // sources, so labels placed near their source separate,
                  // while midpoints collide.
                  x={x1 + (x2 - x1) * 0.35}
                  y={y1 + (y2 - y1) * 0.35 - 6}
                  textAnchor="middle"
                  className="fill-base-content/45 font-mono text-[10px]"
                >
                  {e.calls > 1 ? `${e.calls}x ` : ""}
                  {formatMs(e.maxMs)}
                </text>
              </g>
            );
          })}
          {exitEdges.map(({ exit, from }) => {
            const x1 = from.x + CARD_W;
            const y1 = from.y + CARD_H / 2;
            const x2 = exit.x;
            const y2 = exit.y + CARD_H / 2;
            const mid = (x1 + x2) / 2;
            return (
              <path
                key={`${exit.from} ${exit.target}`}
                d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`}
                fill="none"
                stroke={exit.errorCount > 0 ? "var(--color-error)" : "var(--color-neutral)"}
                strokeWidth={1.5}
                // Dashed, because the far end never confirmed any of this: the
                // call is measured entirely at the caller.
                strokeDasharray="4 3"
                markerEnd="url(#path-arrow)"
              />
            );
          })}
        </svg>

        {nodes.map((n) => (
          <ServiceCard
            key={n.service}
            node={n}
            maxSelf={maxSelf}
            selected={
              selectedSpanId
                ? trace.spans.some(
                    (s) => s.spanId === selectedSpanId && s.service === n.service,
                  )
                : false
            }
            focused={focus === n.service}
            onSelect={() => selectFirstSpan(n.service)}
            onFocus={() => setFocus((prev) => (prev === n.service ? null : n.service))}
          />
        ))}

        {exits.map((e) => (
          <ExitCard
            key={`${e.from} ${e.target}`}
            exit={e}
            onSelect={() => {
              const span = trace.spans.find((s) => s.spanId === e.firstSpanId);
              if (span) onSelectSpan?.(span);
            }}
          />
        ))}
      </div>

      <div className="absolute right-2 top-2 flex gap-1 rounded-lg border border-neutral bg-base-100/90 p-0.5">
        {focus && (
          <Button variant="ghost" size="icon" aria-label="Clear focus" onClick={() => setFocus(null)}>
            <RotateCcw className="h-4 w-4" />
          </Button>
        )}
        <Button variant="ghost" size="icon" aria-label="Zoom in" onClick={zoomIn}>
          <Plus className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Zoom out" onClick={zoomOut}>
          <Minus className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Fit to view" onClick={() => fit(width, height)}>
          <Maximize className="h-4 w-4" />
        </Button>
      </div>

      <div className="absolute left-2 top-2 max-w-[26rem] rounded-md bg-base-100/90 px-2 py-1 text-[10px] leading-relaxed text-base-content/60">
        {focus ? (
          <>
            Showing what <span className="font-mono text-base-content/80">{focus}</span> caused.
          </>
        ) : (
          <>Bar = time spent in the service itself · click a card to select its span</>
        )}
      </div>
    </div>
  );
}

function ServiceCard({
  node,
  maxSelf,
  selected,
  focused,
  onSelect,
  onFocus,
}: {
  node: Placed;
  maxSelf: number;
  selected: boolean;
  focused: boolean;
  onSelect: () => void;
  onFocus: () => void;
}) {
  const bad = node.errorCount > 0;
  return (
    <div
      data-testid={`path-node-${node.service}`}
      className={cn(
        "absolute flex flex-col justify-center gap-1 rounded-lg border bg-base-100 px-2 shadow-sm transition-colors",
        bad ? "border-error/60" : "border-neutral",
        selected && "ring-1 ring-primary",
        focused && "ring-1 ring-primary/60",
      )}
      style={{ left: node.x, top: node.y, width: CARD_W, height: CARD_H }}
    >
      <button onClick={onSelect} className="flex min-w-0 items-center gap-1.5 text-left">
        <span
          className="h-2.5 w-2.5 shrink-0 rounded-sm"
          style={{ backgroundColor: serviceColor(node.service) }}
          aria-hidden
        />
        <span className="truncate font-mono text-xs font-medium">{node.service}</span>
        {node.isEntry && (
          <span className="shrink-0 rounded bg-base-300 px-1 text-[9px] uppercase text-base-content/50">
            entry
          </span>
        )}
      </button>

      <div className="flex items-center gap-1.5 text-[10px] text-base-content/55">
        <span className="font-mono">{formatMs(node.selfMs)}</span>
        <span>self</span>
        <span className="text-base-content/30">·</span>
        <span className="font-mono">{node.spanCount}</span>
        <span>{node.spanCount === 1 ? "span" : "spans"}</span>
        {bad && <span className="ml-auto font-mono text-error">{node.errorCount} err</span>}
        {!bad && node.refusedCount > 0 && (
          <span className="ml-auto font-mono text-warning">{node.refusedCount} 4xx</span>
        )}
      </div>

      {/* Self time as a share of the busiest service on the path: the one
          channel that answers "where did this request actually spend its time". */}
      <div className="h-1 w-full overflow-hidden rounded-full bg-base-300">
        <div
          className={cn("h-full rounded-full", bad ? "bg-error" : "bg-primary")}
          style={{ width: `${Math.round((node.selfMs / maxSelf) * 100)}%` }}
        />
      </div>

      <button
        onClick={onFocus}
        className="absolute -bottom-2 right-2 rounded border border-neutral bg-base-100 px-1 text-[9px] text-base-content/50 hover:text-primary"
        aria-label={focused ? `Clear focus on ${node.service}` : `Focus on ${node.service}`}
      >
        {focused ? "clear" : "focus"}
      </button>
    </div>
  );
}

// A dependency that never sent a span: drawn dashed and outlined, with no
// self-time bar, because every number on it was measured at the caller. Same
// treatment the service map gives a virtual target, for the same reason —
// showing it as a solid node would claim knowledge nobody has.
function ExitCard({ exit, onSelect }: { exit: PlacedExit; onSelect: () => void }) {
  return (
    <button
      onClick={onSelect}
      className={cn(
        "absolute flex flex-col justify-center gap-1 rounded-lg border border-dashed bg-base-100/60 px-2 text-left transition-colors hover:border-base-content/40",
        exit.errorCount > 0 ? "border-error/50" : "border-neutral",
      )}
      style={{ left: exit.x, top: exit.y, width: CARD_W, height: CARD_H }}
      title={`${exit.target} — called by ${exit.from}; timed at the caller`}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="h-2.5 w-2.5 shrink-0 rounded-sm border border-base-content/30" aria-hidden />
        <span className="truncate font-mono text-xs font-medium text-base-content/75">
          {exit.target}
        </span>
      </span>
      <span className="flex items-center gap-1.5 text-[10px] text-base-content/45">
        <span className="font-mono">{formatMs(exit.totalMs)}</span>
        <span>at caller</span>
        {exit.calls > 1 && (
          <>
            <span className="text-base-content/25">·</span>
            <span className="font-mono">{exit.calls} calls</span>
          </>
        )}
        {exit.errorCount > 0 && (
          <span className="ml-auto font-mono text-error">{exit.errorCount} err</span>
        )}
      </span>
      <span className="truncate text-[9px] uppercase tracking-wide text-base-content/30">
        {exit.component ? `${exit.component} · no telemetry` : "no telemetry of its own"}
      </span>
    </button>
  );
}
