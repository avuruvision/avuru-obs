"use client";

import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner, Spinner } from "@/components/ui/spinner";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
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
}: {
  pages?: TraceSummary[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  onSelect: (traceId: string) => void;
  selectedTraceId?: string;
}) {
  if (isLoading) return <CenteredSpinner />;
  const traces = pages?.flat() ?? [];
  if (!traces.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No traces match these filters in this window.
      </Card>
    );
  }

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
          </tr>
        </thead>
        <tbody>
          {traces.map((t) => (
            <tr
              key={t.traceId}
              onClick={() => onSelect(t.traceId)}
              className={cn(
                "cursor-pointer border-b border-neutral/40 transition-colors last:border-0",
                t.traceId === selectedTraceId
                  ? "bg-primary/10"
                  : "hover:bg-base-300/50",
              )}
            >
              <td className="font-mono text-xs" title={utcTooltip(t.startTime)}>
                {formatTime(t.startTime)}
              </td>
              <td className="font-medium text-primary">{t.rootService}</td>
              <td className="max-w-64 truncate font-mono text-xs">
                {t.rootOperation}
              </td>
              <td className="text-right font-mono text-xs">
                {formatMs(t.durationMs)}
              </td>
              <td className="text-right font-mono text-xs">{t.spanCount}</td>
              <td className="text-right">
                {t.errorCount > 0 ? (
                  <Badge tone="error">{t.errorCount}</Badge>
                ) : (
                  <span className="text-base-content/40">—</span>
                )}
              </td>
            </tr>
          ))}
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
