"use client";

import { useMemo } from "react";
import { ArrowDownLeft, ArrowUpRight, Waypoints } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { ServiceEdge } from "@/lib/api-types";

// One direction of a service's neighbourhood.
//
// Callers and callees are shown apart rather than as one "connections" list:
// they are read for opposite reasons — who is affected when this service breaks,
// against what could be breaking it — and merging them makes both harder.
function DependencyTable({
  title,
  icon: Icon,
  edges,
  peerOf,
  windowMs,
  onSelect,
  emptyHint,
}: {
  title: string;
  icon: typeof ArrowUpRight;
  edges: ServiceEdge[];
  peerOf: (e: ServiceEdge) => string;
  windowMs: number;
  onSelect: (service: string) => void;
  emptyHint: string;
}) {
  return (
    <Card className="min-w-0 flex-1">
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <Icon className="h-3.5 w-3.5 text-base-content/40" aria-hidden />
          {title}
        </CardTitle>
        <span className="text-xs text-base-content/50">{edges.length}</span>
      </CardHeader>
      {edges.length === 0 ? (
        <p className="px-4 pb-4 text-xs text-base-content/45">{emptyHint}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="text-left text-base-content/50">
              <tr className="border-b border-neutral">
                <th className="px-4 py-2 font-medium">Service</th>
                <th className="px-4 py-2 text-right font-medium">Rate</th>
                <th className="px-4 py-2 text-right font-medium">Errors</th>
                <th className="px-4 py-2 text-right font-medium">p95</th>
              </tr>
            </thead>
            <tbody>
              {edges.map((e) => {
                const peer = peerOf(e);
                return (
                  <tr
                    key={`${e.source}->${e.target}`}
                    className="cursor-pointer border-b border-neutral/50 last:border-0 hover:bg-base-300/40"
                    onClick={() => onSelect(peer)}
                  >
                    <td className="max-w-[14rem] truncate px-4 py-1.5 font-mono">
                      {peer}
                      {e.viaTransport?.length ? (
                        // The dependency is real but the hub had to walk the
                        // trace's parent chain over a proxy to see it. Saying so
                        // is the difference between a fact and a guess.
                        <span
                          className="ml-1.5 inline-flex items-center gap-0.5 text-[10px] text-base-content/40"
                          title={`recovered across ${e.viaTransport.join(", ")}`}
                        >
                          <Waypoints className="h-3 w-3" aria-hidden />
                          via {e.viaTransport.join(", ")}
                        </span>
                      ) : null}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {formatRate(e.calls / (windowMs / 1000))}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {e.errorCount > 0 ? (
                        <span className="text-error">{formatPercent(e.errorRate)}</span>
                      ) : (
                        <span className="text-base-content/30">—</span>
                      )}
                    </td>
                    <td
                      className={cn(
                        "px-4 py-1.5 text-right font-mono",
                        e.p95Ms === undefined && "text-base-content/30",
                      )}
                    >
                      {/* Absent means "not measured" — a flow-derived edge has
                          no span to time — and must never render as 0ms. */}
                      {e.p95Ms === undefined ? "—" : formatMs(e.p95Ms)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

// A service's neighbourhood, derived from the map's own edge set so the two
// screens cannot disagree about what depends on what — the hop-collapse and
// transport classification are already applied in that response.
export function ServiceDependencies({
  service,
  edges,
  windowMs,
  onSelect,
}: {
  service: string;
  edges: ServiceEdge[];
  windowMs: number;
  onSelect: (service: string) => void;
}) {
  const { upstream, downstream } = useMemo(() => {
    const byCalls = (a: ServiceEdge, b: ServiceEdge) => b.calls - a.calls;
    return {
      upstream: edges.filter((e) => e.target === service).sort(byCalls),
      downstream: edges.filter((e) => e.source === service).sort(byCalls),
    };
  }, [edges, service]);

  return (
    <div className="flex flex-col gap-3 lg:flex-row">
      <DependencyTable
        title="Called by"
        icon={ArrowDownLeft}
        edges={upstream}
        peerOf={(e) => e.source}
        windowMs={windowMs}
        onSelect={onSelect}
        emptyHint="Nothing observed calling this service in this window. It may be an entry point, or its callers may not be instrumented."
      />
      <DependencyTable
        title="Depends on"
        icon={ArrowUpRight}
        edges={downstream}
        peerOf={(e) => e.target}
        windowMs={windowMs}
        onSelect={onSelect}
        emptyHint="No outgoing calls observed in this window."
      />
    </div>
  );
}
