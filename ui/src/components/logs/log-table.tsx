"use client";

import Link from "next/link";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { CenteredSpinner, Spinner } from "@/components/ui/spinner";
import { SeverityBadge } from "./severity-badge";
import { formatTime, utcTooltip } from "@/lib/format";
import type { LogRecord } from "@/lib/api-types";

export function LogTable({
  pages,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
}: {
  pages?: LogRecord[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
}) {
  if (isLoading) return <CenteredSpinner />;
  const logs = pages?.flat() ?? [];
  if (!logs.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No logs match these filters in this window. Container stdout/stderr and
        OTLP logs appear here, correlated to traces by{" "}
        <code className="rounded bg-base-300 px-1">trace_id</code>.
      </Card>
    );
  }

  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Time</th>
            <th>Severity</th>
            <th>Service</th>
            <th>Message</th>
            <th className="text-right">Trace</th>
          </tr>
        </thead>
        <tbody>
          {logs.map((l, i) => (
            <tr
              key={`${l.timestamp}-${l.spanId}-${i}`}
              className="border-b border-neutral/40 align-top transition-colors last:border-0 hover:bg-base-300/50"
            >
              <td className="whitespace-nowrap font-mono text-xs" title={utcTooltip(l.timestamp)}>
                {formatTime(l.timestamp)}
              </td>
              <td>
                <SeverityBadge severity={l.severity} />
              </td>
              <td className="whitespace-nowrap font-medium text-primary">{l.service}</td>
              <td className="font-mono text-xs">{l.body}</td>
              <td className="text-right">
                {l.traceId ? (
                  <Link
                    href={`/traces?trace=${l.traceId}&tab=traces`}
                    className="font-mono text-xs text-primary hover:underline"
                    title="Open correlated trace"
                  >
                    {l.traceId.slice(0, 8)}
                  </Link>
                ) : (
                  <span className="text-base-content/30">—</span>
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
