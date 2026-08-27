"use client";

import { useMemo } from "react";
import { PieChart } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { formatMs, formatPercent } from "@/lib/format";
import { groupColors, OTHER_COLOR } from "@/lib/series-color";
import { useTraceBreakdown, type TraceFilters } from "@/hooks/use-traces-data";
import type { TimeParams } from "@/lib/query-keys";
import { Treemap, type TreemapDatum } from "./treemap";
import { Donut } from "./donut";
import {
  drillFilter,
  GROUP_BY_OPTIONS,
  SCOPE_HELP,
  SCOPE_OPTIONS,
} from "./breakdown-controls";

// How many slices the charts draw. Past this the palette would have to invent
// hues, so the rest folds into the tail bucket the API already computes - while
// the table below keeps the full top-N, where a long list costs nothing.
const CHART_SLICES = 8;
const TABLE_ROWS = 20;

const WEIGHT_OPTIONS = [
  { value: "count", label: "By requests" },
  { value: "time", label: "By total time" },
];

// The label for a value that has none. Spans carrying no value for the grouping
// dimension are a real answer - "how much of my traffic is unlabelled" is what a
// tagging rollout is asking - so they are named, not hidden.
const UNSET_LABEL = "(not set)";

export function BreakdownPanel({
  time,
  filters,
  groupBy,
  scope,
  weight,
  onControlChange,
  onDrill,
}: {
  time: TimeParams;
  filters: TraceFilters;
  groupBy: string;
  scope: string;
  weight: "count" | "time";
  onControlChange: (next: Record<string, string | undefined>) => void;
  onDrill: (params: Record<string, string>) => void;
}) {
  const { data, isLoading, error } = useTraceBreakdown(time, filters, groupBy, scope, TABLE_ROWS);

  // One ordered list feeds the treemap, the donut, the legend and the table, so
  // the four can never disagree about what is in the window.
  const rows = useMemo<TreemapDatum[]>(() => {
    if (!data) return [];
    const shown = data.groups.slice(0, CHART_SLICES);
    // Everything the charts do not draw: the API's tail plus the groups past
    // the slice budget. Folding them together keeps the parts summing to the
    // whole - a chart that quietly drops its tail redraws a top-8 as the estate.
    const spilled = data.groups.slice(CHART_SLICES);
    const tail = [...spilled, ...(data.other ? [data.other] : [])];
    const colors = groupColors(
      groupBy,
      shown.map((g) => g.key),
    );
    const out: TreemapDatum[] = shown.map((g) => ({
      ...g,
      color: colors.get(g.key) ?? OTHER_COLOR,
      label: g.key || UNSET_LABEL,
    }));
    if (tail.length) {
      const merged = tail.reduce(
        (acc, g) => ({
          key: "",
          count: acc.count + g.count,
          errorCount: acc.errorCount + g.errorCount,
          errorRate: 0,
          refusedCount: (acc.refusedCount ?? 0) + (g.refusedCount ?? 0),
          durationMsSum: acc.durationMsSum + g.durationMsSum,
        }),
        { key: "", count: 0, errorCount: 0, errorRate: 0, refusedCount: 0, durationMsSum: 0 },
      );
      out.push({
        ...merged,
        errorRate: merged.count > 0 ? merged.errorCount / merged.count : 0,
        color: OTHER_COLOR,
        label: `Other (${tail.length})`,
      });
    }
    return out;
  }, [data, groupBy]);

  const total = data?.total;
  const weightOf = (d: TreemapDatum) => (weight === "count" ? d.count : d.durationMsSum);
  const weightTotal = rows.reduce((sum, d) => sum + weightOf(d), 0);

  // A slice is only clickable when the trace list can be filtered to exactly
  // what it represents; see drillFilter for why some dimensions cannot.
  const select = (key: string) => {
    if (!key) return; // the tail is not a value
    const params = drillFilter(groupBy, key);
    if (params) onDrill(params);
  };
  const canDrill = Boolean(drillFilter(groupBy, "probe"));

  return (
    <div className="flex flex-col gap-3">
      <Card>
        <CardHeader className="flex-wrap gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle>Breakdown</CardTitle>
            <Select
              value={groupBy}
              options={GROUP_BY_OPTIONS}
              onChange={(v) => onControlChange({ groupBy: v })}
              ariaLabel="Group by"
              className="w-44"
            />
            <Select
              value={scope}
              options={SCOPE_OPTIONS}
              onChange={(v) => onControlChange({ scope: v })}
              ariaLabel="Span scope"
              className="w-48"
            />
            <Select
              value={weight}
              options={WEIGHT_OPTIONS}
              onChange={(v) => onControlChange({ weight: v })}
              ariaLabel="Weight"
              className="w-40"
            />
          </div>
          {total && total.count > 0 && (
            <span className="font-mono text-xs text-base-content/50">
              {total.count.toLocaleString()} spans
              {data && data.groupCount > rows.length
                ? ` across ${data.groupCount.toLocaleString()} values`
                : ""}
            </span>
          )}
        </CardHeader>
        <p className="px-4 pb-2 text-xs text-base-content/50">{SCOPE_HELP[scope]}</p>

        <div className="px-4 pb-4">
          {isLoading ? (
            <CenteredSpinner />
          ) : error ? (
            <EmptyState icon={PieChart} title="Couldn't reach the hub">
              The breakdown could not be loaded for this window.
            </EmptyState>
          ) : !rows.length ? (
            <EmptyState icon={PieChart} title="Nothing in this window">
              No spans match these filters. Widen the time range, or clear a
              filter.
            </EmptyState>
          ) : (
            <div className="flex flex-col gap-4 lg:flex-row">
              <div className="min-w-0 flex-1">
                <Treemap data={rows} weight={weight} onSelect={canDrill ? select : undefined} />
              </div>
              <div className="flex items-start gap-4 lg:w-80 lg:shrink-0">
                <Donut data={rows} weight={weight} onSelect={canDrill ? select : undefined} />
                <ul className="flex min-w-0 flex-1 flex-col gap-1">
                  {rows.map((d) => (
                    <li key={d.key || "__other__"} className="flex items-center gap-2 text-xs">
                      <span
                        className="h-2.5 w-2.5 shrink-0 rounded-sm"
                        style={{ backgroundColor: d.color }}
                        aria-hidden
                      />
                      <span className="truncate text-base-content/80" title={d.label}>
                        {d.label}
                      </span>
                      <span className="ml-auto shrink-0 font-mono text-base-content/50">
                        {formatPercent(weightTotal > 0 ? weightOf(d) / weightTotal : 0)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          )}
        </div>
      </Card>

      {data && data.groups.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Exact numbers</CardTitle>
            <span className="text-xs text-base-content/50">
              top {Math.min(data.groups.length, TABLE_ROWS)}
              {canDrill ? " · click a row for its traces" : ""}
            </span>
          </CardHeader>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead className="text-left text-base-content/50">
                <tr className="border-b border-neutral">
                  <th className="px-4 py-2 font-medium">Value</th>
                  <th className="px-4 py-2 text-right font-medium">Spans</th>
                  <th className="px-4 py-2 text-right font-medium">Share</th>
                  <th className="px-4 py-2 text-right font-medium">Errors</th>
                  <th className="px-4 py-2 text-right font-medium">Refused</th>
                  <th className="px-4 py-2 text-right font-medium">p95</th>
                  <th className="px-4 py-2 text-right font-medium">Total time</th>
                </tr>
              </thead>
              <tbody>
                {data.groups.map((g) => (
                  <tr
                    key={g.key || "__unset__"}
                    className={
                      canDrill && g.key
                        ? "cursor-pointer border-b border-neutral/50 hover:bg-base-300/40"
                        : "border-b border-neutral/50"
                    }
                    onClick={canDrill && g.key ? () => select(g.key) : undefined}
                  >
                    <td className="max-w-xs truncate px-4 py-1.5 font-mono">
                      {g.key || UNSET_LABEL}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {g.count.toLocaleString()}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono text-base-content/60">
                      {formatPercent(total && total.count > 0 ? g.count / total.count : 0)}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {g.errorCount > 0 ? (
                        <span className="text-error">{formatPercent(g.errorRate)}</span>
                      ) : (
                        <span className="text-base-content/30">—</span>
                      )}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {g.refusedCount ? (
                        <span className="text-warning">{formatPercent(g.refusedRate ?? 0)}</span>
                      ) : (
                        <span className="text-base-content/30">—</span>
                      )}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {g.p95Ms !== undefined ? formatMs(g.p95Ms) : "—"}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono text-base-content/60">
                      {formatMs(g.durationMsSum)}
                    </td>
                  </tr>
                ))}
                {data.other && (
                  <tr className="border-b border-neutral/50 text-base-content/50">
                    <td className="px-4 py-1.5 font-mono italic">
                      everything else ({(data.groupCount - data.groups.length).toLocaleString()}{" "}
                      values)
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {data.other.count.toLocaleString()}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {formatPercent(
                        total && total.count > 0 ? data.other.count / total.count : 0,
                      )}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {data.other.errorCount > 0 ? formatPercent(data.other.errorRate) : "—"}
                    </td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {data.other.refusedCount
                        ? formatPercent(data.other.refusedRate ?? 0)
                        : "—"}
                    </td>
                    {/* Quantiles cannot be recovered for a merged tail. */}
                    <td className="px-4 py-1.5 text-right font-mono">—</td>
                    <td className="px-4 py-1.5 text-right font-mono">
                      {formatMs(data.other.durationMsSum)}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}
