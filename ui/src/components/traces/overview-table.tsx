"use client";

import { useMemo } from "react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { OperationStats } from "@/lib/api-types";

// RED table per (service, operation) over entry spans — Coroot's overview
// density. Rows come grouped by service from the backend (busiest operation
// first within a service); with more than one service we render a section
// header per service, ordered by total request count. Row click filters the
// trace list to that operation.
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
  const groups = useMemo(() => {
    const byService = new Map<string, OperationStats[]>();
    for (const op of operations ?? []) {
      const list = byService.get(op.service);
      if (list) list.push(op);
      else byService.set(op.service, [op]);
    }
    return [...byService.entries()]
      .map(([service, ops]) => ({
        service,
        ops,
        count: ops.reduce((sum, op) => sum + op.count, 0),
      }))
      .sort((a, b) => b.count - a.count);
  }, [operations]);

  if (isLoading) return <CenteredSpinner />;
  if (!operations?.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No requests in this window. Point an OTel SDK at{" "}
        <code className="rounded bg-base-300 px-1">localhost:4318</code> or
        click around the demo app.
      </Card>
    );
  }

  const windowSec = windowMs / 1000;
  const showHeaders = groups.length > 1;

  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Service</th>
            <th>Operation</th>
            <th className="text-right">Requests</th>
            <th className="text-right">Errors</th>
            <th className="text-right">Refused</th>
            <th className="text-right">p50</th>
            <th className="text-right">p95</th>
            <th className="text-right">p99</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((group) => (
            <GroupRows
              key={group.service}
              group={group}
              showHeader={showHeaders}
              windowSec={windowSec}
              onSelect={onSelect}
              selected={selected}
            />
          ))}
        </tbody>
      </table>
    </Card>
  );
}

function GroupRows({
  group,
  showHeader,
  windowSec,
  onSelect,
  selected,
}: {
  group: { service: string; ops: OperationStats[]; count: number };
  showHeader: boolean;
  windowSec: number;
  onSelect: (service: string, operation: string) => void;
  selected?: { service?: string; operation?: string };
}) {
  return (
    <>
      {showHeader && (
        <tr className="border-b border-neutral/40 bg-base-300/40">
          <td colSpan={8} className="py-1.5 text-xs">
            <span className="font-semibold text-base-content/80">
              {group.service}
            </span>
            <span className="ml-2 font-mono text-base-content/50">
              {formatRate(group.count / windowSec)}
            </span>
          </td>
        </tr>
      )}
      {group.ops.map((op) => {
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
            {/* Server-side 4xx, amber and its own column. An operation that
                turns most of its callers away reads at a glance here, without
                any of it reaching the error rate alerting fires on. */}
            <td className="text-right">
              {(op.refusedCount ?? 0) > 0 ? (
                <Badge tone="warning">{formatPercent(op.refusedRate ?? 0)}</Badge>
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
    </>
  );
}
