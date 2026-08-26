"use client";

import { Fragment, useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner, Spinner } from "@/components/ui/spinner";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { cn } from "@/lib/cn";
import type { TraceSummary } from "@/lib/api-types";

export function TraceList({
  pages,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  onSelect,
  selectedTraceId,
  groupByService,
}: {
  pages?: TraceSummary[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  onSelect: (traceId: string) => void;
  selectedTraceId?: string;
  groupByService?: boolean;
}) {
  const traces = useMemo(() => pages?.flat() ?? [], [pages]);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());

  // Group the loaded traces by root service (busiest first), preserving the
  // server order within each service. Same grouping shape as the Overview tab;
  // only used when the "Group by service" toggle is on. Grouping applies to the
  // pages loaded so far — "Load more" appends and regroups.
  const groups = useMemo(() => {
    const byService = new Map<string, TraceSummary[]>();
    for (const t of traces) {
      const list = byService.get(t.rootService);
      if (list) list.push(t);
      else byService.set(t.rootService, [t]);
    }
    return [...byService.entries()]
      .map(([service, items]) => ({
        service,
        items,
        errors: items.reduce((n, t) => n + (t.errorCount > 0 ? 1 : 0), 0),
      }))
      .sort((a, b) => b.items.length - a.items.length);
  }, [traces]);

  if (isLoading) return <CenteredSpinner />;
  if (!traces.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No traces match these filters in this window.
      </Card>
    );
  }

  const toggle = (service: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(service)) next.delete(service);
      else next.add(service);
      return next;
    });

  const row = (t: TraceSummary) => (
    <tr
      key={t.traceId}
      onClick={() => onSelect(t.traceId)}
      className={cn(
        "cursor-pointer border-b border-neutral/40 transition-colors last:border-0",
        t.traceId === selectedTraceId ? "bg-primary/10" : "hover:bg-base-300/50",
      )}
    >
      <td className="font-mono text-xs" title={utcTooltip(t.startTime)}>
        {formatTime(t.startTime)}
      </td>
      <td className="font-medium text-primary">{t.rootService}</td>
      <td className="max-w-64 truncate font-mono text-xs">{t.rootOperation}</td>
      <td className="text-right font-mono text-xs">{formatMs(t.durationMs)}</td>
      <td className="text-right font-mono text-xs">{t.spanCount}</td>
      <td className="text-right">
        {t.errorCount > 0 ? (
          <Badge tone="error">{t.errorCount}</Badge>
        ) : (
          <span className="text-base-content/40">—</span>
        )}
      </td>
      {/* Server-side 4xx. Its own column for the same reason it is its own
          class: a trace whose only span was refused is not a failing trace,
          and it is not a clean one either. */}
      <td className="text-right">
        {(t.refusedCount ?? 0) > 0 ? (
          <Badge tone="warning">{t.refusedCount}</Badge>
        ) : (
          <span className="text-base-content/40">—</span>
        )}
      </td>
    </tr>
  );

  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Time</th>
            <th>Root service</th>
            <th>Root span</th>
            <th className="text-right">Duration</th>
            <th className="text-right">Spans</th>
            <th className="text-right">Errors</th>
            <th className="text-right">Refused</th>
          </tr>
        </thead>
        <tbody>
          {groupByService
            ? groups.map((g) => {
                const isCollapsed = collapsed.has(g.service);
                return (
                  <Fragment key={g.service}>
                    <tr
                      onClick={() => toggle(g.service)}
                      className="cursor-pointer border-b border-neutral/40 bg-base-300/40 hover:bg-base-300/60"
                    >
                      <td colSpan={7} className="py-1.5 text-xs">
                        <span className="inline-flex items-center gap-2">
                          {isCollapsed ? (
                            <ChevronRight className="h-3.5 w-3.5 text-base-content/50" />
                          ) : (
                            <ChevronDown className="h-3.5 w-3.5 text-base-content/50" />
                          )}
                          <span
                            className="h-2 w-2 shrink-0 rounded-full"
                            style={{ backgroundColor: serviceColor(g.service) }}
                          />
                          <span className="font-semibold text-base-content/80">
                            {g.service}
                          </span>
                          <span className="font-mono text-base-content/50">
                            · {g.items.length} trace{g.items.length === 1 ? "" : "s"}
                            {g.errors > 0 && ` · ${g.errors} err`}
                          </span>
                        </span>
                      </td>
                    </tr>
                    {!isCollapsed && g.items.map(row)}
                  </Fragment>
                );
              })
            : traces.map(row)}
        </tbody>
      </table>
      {hasNextPage && (
        <div className="border-t border-neutral p-2 text-center">
          <Button variant="ghost" size="sm" onClick={fetchNextPage} disabled={isFetchingNextPage}>
            {isFetchingNextPage ? <Spinner className="h-4 w-4" /> : "Load more"}
          </Button>
        </div>
      )}
    </Card>
  );
}
