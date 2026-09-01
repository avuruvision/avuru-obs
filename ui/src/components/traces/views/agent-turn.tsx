"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Maximize, Minus, Plus, Wrench } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { usePanZoom } from "@/hooks/use-pan-zoom";
import {
  agentTurnRoots,
  buildAgentTurn,
  type TurnNode,
} from "@/lib/agent-turn";
import type { Span, TraceResponse } from "@/lib/api-types";

const CARD_W = 180;
const CARD_H = 62;
const COL_GAP = 96;
const ROW_GAP = 20;
const PAD = 28;

interface Placed extends TurnNode {
  x: number;
  y: number;
}

// One agent turn, drawn as the graph it is.
//
// Distinct from its neighbours in this panel for the same reason the Path view
// is: the Tree is per SPAN, and a turn that looped over a tool six times is six
// rows saying the same thing. The Path view is per SERVICE, and every span of a
// turn usually belongs to ONE service — so it collapses the whole turn into a
// single node. The unit here is the model or tool being called, which is what
// the questions are actually about.
export function AgentTurnView({
  trace,
  selectedSpanId,
  onSelectSpan,
}: {
  trace: TraceResponse;
  selectedSpanId?: string | null;
  onSelectSpan?: (span: Span) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const roots = useMemo(() => agentTurnRoots(trace.spans), [trace.spans]);
  const [rootId, setRootId] = useState<string | null>(null);

  // A trace can hold several turns; default to the first and let the user pick.
  const activeRoot =
    rootId && roots.some((r) => r.spanId === rootId)
      ? rootId
      : roots[0]?.spanId;

  const turn = useMemo(
    () => (activeRoot ? buildAgentTurn(trace.spans, activeRoot) : null),
    [trace.spans, activeRoot],
  );

  const { nodes, edges, width, height, maxSelf } = useMemo(() => {
    const list = turn?.nodes ?? [];
    // Columns are hops from the agent: the agent, then what it called, then
    // what those called. Depth comes from the edges rather than from span
    // nesting, so a tool reached through an uninstrumented client span still
    // sits one hop from the model that asked for it.
    const depth = new Map<string, number>();
    for (const n of list) if (n.kind === "agent") depth.set(n.id, 0);
    const turnEdges = turn?.edges ?? [];
    // Relaxation rather than recursion: the graph is tiny and may contain a
    // cycle when an agent invokes itself, which a naive walk would not survive.
    for (let pass = 0; pass < list.length + 1; pass++) {
      for (const e of turnEdges) {
        const from = depth.get(e.source);
        if (from === undefined) continue;
        const next = from + 1;
        if ((depth.get(e.target) ?? Infinity) > next) depth.set(e.target, next);
      }
    }
    // Anything the edges never reached (a model call with no agent parent in
    // this trace) still has to be drawn, so it lands in column 0 beside it.
    for (const n of list) if (!depth.has(n.id)) depth.set(n.id, 0);

    const columns = new Map<number, TurnNode[]>();
    for (const n of list) {
      const d = depth.get(n.id) ?? 0;
      columns.set(d, [...(columns.get(d) ?? []), n]);
    }
    const depths = [...columns.keys()].sort((a, b) => a - b);
    const tallest = Math.max(
      ...depths.map((d) => columns.get(d)?.length ?? 0),
      1,
    );

    const placed: Placed[] = [];
    depths.forEach((d, col) => {
      const cells = (columns.get(d) ?? [])
        .slice()
        .sort((a, b) => a.step - b.step);
      const offset = ((tallest - cells.length) * (CARD_H + ROW_GAP)) / 2;
      const x = PAD + col * (CARD_W + COL_GAP);
      cells.forEach((n, row) => {
        placed.push({ ...n, x, y: PAD + offset + row * (CARD_H + ROW_GAP) });
      });
    });

    const byId = new Map(placed.map((p) => [p.id, p]));
    return {
      nodes: placed,
      edges: turnEdges
        .filter((e) => byId.has(e.source) && byId.has(e.target))
        .map((e) => ({
          ...e,
          from: byId.get(e.source) as Placed,
          to: byId.get(e.target) as Placed,
        })),
      width:
        PAD * 2 + Math.max(depths.length, 1) * (CARD_W + COL_GAP) - COL_GAP,
      height: PAD * 2 + tallest * (CARD_H + ROW_GAP) - ROW_GAP,
      maxSelf: Math.max(...placed.map((p) => p.selfMs), 1),
    };
  }, [turn]);

  const { transform, handlers, zoomIn, zoomOut, fit } =
    usePanZoom(containerRef);

  useEffect(() => {
    fit(width, height);
  }, [fit, width, height]);

  if (!turn) {
    return (
      <div className="rounded-lg border border-neutral bg-base-200 p-8 text-center text-sm text-base-content/55">
        No agent turn in this trace. A turn is a span carrying{" "}
        <code className="font-mono text-xs">
          gen_ai.operation.name=invoke_agent
        </code>
        .
      </div>
    );
  }

  const select = (spanId: string) => {
    const span = trace.spans.find((s) => s.spanId === spanId);
    if (span) onSelectSpan?.(span);
  };

  return (
    <div className="space-y-2">
      {roots.length > 1 && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="text-base-content/55">Turn:</span>
          {roots.map((r, i) => (
            <Button
              key={r.spanId}
              variant={r.spanId === activeRoot ? "primary" : "ghost"}
              size="sm"
              onClick={() => setRootId(r.spanId)}
            >
              {i + 1}
            </Button>
          ))}
        </div>
      )}

      <div
        ref={containerRef}
        {...handlers}
        data-testid="agent-turn"
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
          <svg
            width={width}
            height={height}
            className="absolute left-0 top-0 overflow-visible"
          >
            <defs>
              <marker
                id="turn-arrow"
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
                    stroke={
                      e.errorCount > 0
                        ? "var(--color-error)"
                        : "var(--color-neutral)"
                    }
                    strokeWidth={1.5}
                    markerEnd="url(#turn-arrow)"
                  />
                  {e.calls > 1 && (
                    <text
                      x={x1 + (x2 - x1) * 0.35}
                      y={y1 + (y2 - y1) * 0.35 - 6}
                      textAnchor="middle"
                      className="fill-base-content/45 font-mono text-[10px]"
                    >
                      {e.calls}x
                    </text>
                  )}
                </g>
              );
            })}
          </svg>

          {nodes.map((n) => (
            <TurnCard
              key={n.id}
              node={n}
              maxSelf={maxSelf}
              selected={selectedSpanId === n.firstSpanId}
              onSelect={() => select(n.firstSpanId)}
            />
          ))}
        </div>

        <div className="absolute right-2 top-2 flex gap-1 rounded-lg border border-neutral bg-base-100/90 p-0.5">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Zoom in"
            onClick={zoomIn}
          >
            <Plus className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Zoom out"
            onClick={zoomOut}
          >
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

        <div className="absolute left-2 top-2 max-w-[26rem] rounded-md bg-base-100/90 px-2 py-1 text-[10px] leading-relaxed text-base-content/60">
          Bar = time spent in the call itself · a tool hit twice is one node
          with a count
        </div>
      </div>
    </div>
  );
}

function TurnCard({
  node,
  maxSelf,
  selected,
  onSelect,
}: {
  node: Placed;
  maxSelf: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const bad = node.errorCount > 0;
  const Icon = node.kind === "tool" ? Wrench : Bot;
  return (
    <button
      type="button"
      onClick={onSelect}
      title={node.label}
      className={cn(
        "absolute flex flex-col justify-center gap-1 rounded-lg border bg-base-100 px-2 text-left shadow-sm transition-colors",
        bad ? "border-error/60" : "border-neutral",
        selected && "ring-2 ring-primary",
        // The agent node is the turn itself rather than a call inside it, so it
        // reads as a container: dashed, to say it holds the others.
        node.kind === "agent" && "border-dashed",
      )}
      style={{ left: node.x, top: node.y, width: CARD_W, height: CARD_H }}
    >
      <div className="flex items-center gap-1.5">
        <Icon
          className={cn(
            "h-3 w-3 shrink-0",
            node.kind === "tool" ? "text-warning" : "text-primary",
          )}
          aria-hidden
        />
        <span className="truncate font-mono text-xs">{node.label}</span>
        {node.calls > 1 && (
          <span className="ml-auto shrink-0 rounded bg-base-300 px-1 font-mono text-[10px] text-base-content/70">
            {node.calls}x
          </span>
        )}
      </div>
      <div className="flex items-center gap-1.5 text-[10px] text-base-content/55">
        <span className="tabular-nums">{formatMs(node.selfMs)}</span>
        {bad && <span className="text-error">{node.errorCount} failed</span>}
      </div>
      {/* Self time, not duration: a model span contains the tool spans it
          triggered, so a duration bar would show the model as responsible for
          time the tools spent. */}
      <div className="h-1 w-full overflow-hidden rounded-full bg-base-300">
        <div
          className={cn("h-full rounded-full", bad ? "bg-error" : "bg-primary")}
          style={{ width: `${Math.max((node.selfMs / maxSelf) * 100, 2)}%` }}
        />
      </div>
    </button>
  );
}
