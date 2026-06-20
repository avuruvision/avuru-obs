"use client";

import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { OperationStats } from "@/lib/api-types";

// RED table per (root service, operation) — Coroot's overview density.
// Row click filters the trace list to that operation.
export function OverviewTable({
  operations,
  isLoading,
  windowMs,
  onSelect,
  selected,
}: {
  operations?: OperationStats[];
  isLoading: boolean;
  windowMs: number;
  onSelect: (service: string, operation: string) => void;
  selected?: { service?: string; operation?: string };
}) {
  if (isLoading) return <CenteredSpinner />;
  if (!operations?.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No root spans in this window. Point an OTel SDK at{" "}
        <code className="rounded bg-base-300 px-1">localhost:4318</code> or
        click around the demo app.
      </Card>
    );
  }

  const windowSec = windowMs / 1000;

  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Root service</th>
            <th>Root span</th>
            <th className="text-right">Requests</th>
            <th className="text-right">Errors</th>
            <th className="text-right">p50</th>
            <th className="text-right">p95</th>
            <th className="text-right">p99</th>
          </tr>
        </thead>
        <tbody>
          {operations.map((op) => {
            const isSelected =
              selected?.service === op.service &&
              selected?.operation === op.operation;
            return (
              <tr
                key={`${op.service}:${op.operation}`}
                onClick={() => onSelect(op.service, op.operation)}
                className={cn(
                  "cursor-pointer border-b border-neutral/40 transition-colors last:border-0",
                  isSelected ? "bg-primary/10" : "hover:bg-base-300/50",
                )}
              >
                <td className="font-medium text-primary">{op.service}</td>
                <td className="max-w-64 truncate font-mono text-xs">
                  {op.operation}
                </td>
                <td className="text-right font-mono text-xs">
                  {formatRate(op.count / windowSec)}
                </td>
                <td className="text-right">
                  {op.errorCount > 0 ? (
                    <Badge tone="error">{formatPercent(op.errorRate)}</Badge>
                  ) : (
                    <span className="text-base-content/40">—</span>
                  )}
                </td>
                <td className="text-right font-mono text-xs">{formatMs(op.p50Ms)}</td>
                <td className="text-right font-mono text-xs">{formatMs(op.p95Ms)}</td>
                <td className="text-right font-mono text-xs">{formatMs(op.p99Ms)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Card>
  );
}
