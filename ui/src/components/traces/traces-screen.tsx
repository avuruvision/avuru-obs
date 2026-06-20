"use client";

import { useMemo } from "react";
import { FilterX } from "lucide-react";
import { Tabs } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import {
  useHeatmap,
  useTraceOverview,
  useTraceSearch,
  type TraceFilters,
} from "@/hooks/use-traces-data";
import { formatMs } from "@/lib/format";
import { Heatmap } from "./heatmap";
import { OverviewTable } from "./overview-table";
import { TraceList } from "./trace-list";
import { TraceDetail } from "./trace-detail";

type Tab = "overview" | "traces";

// Coroot flow: heatmap on top, tabs below; every filter and the selected
// trace live in the URL.
export function TracesScreen() {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();

  const tab: Tab = get("tab") === "traces" ? "traces" : "overview";
  const selectedTrace = get("trace") ?? null;
  const filters: TraceFilters = useMemo(
    () => ({
      service: get("service"),
      operation: get("operation"),
      status: get("status"),
      minDurationMs: get("minMs") ? Number(get("minMs")) : undefined,
      maxDurationMs: get("maxMs") ? Number(get("maxMs")) : undefined,
    }),
    [get],
  );
  const hasFilters = Boolean(
    filters.service || filters.operation || filters.status || filters.minDurationMs || filters.maxDurationMs,
  );

  const overview = useTraceOverview(time, filters.service);
  const heatmap = useHeatmap(time, filters);
  const search = useTraceSearch(time, filters);

  return (
    <div className="flex flex-col gap-4">
      <Heatmap
        data={heatmap.data}
        isLoading={heatmap.isLoading}
        onSelectBand={(minMs, maxMs) =>
          setMany({ minMs: String(minMs), maxMs: String(maxMs), tab: "traces" })
        }
      />

      {selectedTrace && (
        <TraceDetail traceId={selectedTrace} onClose={() => setMany({ trace: undefined })} />
      )}

      <div className="flex items-center justify-between">
        <Tabs<Tab>
          items={[
            { value: "overview", label: "Overview" },
            { value: "traces", label: "Traces" },
          ]}
          value={tab}
          onChange={(t) => setMany({ tab: t })}
        />
        {hasFilters && (
          <div className="flex items-center gap-1.5">
            {filters.service && <Badge tone="primary">{filters.service}</Badge>}
            {filters.operation && <Badge tone="neutral">{filters.operation}</Badge>}
            {filters.status && <Badge tone={filters.status === "error" ? "error" : "success"}>{filters.status}</Badge>}
            {filters.minDurationMs !== undefined && (
              <Badge tone="warning">
                {formatMs(filters.minDurationMs)}–{formatMs(filters.maxDurationMs ?? Infinity)}
              </Badge>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                setMany({ service: undefined, operation: undefined, status: undefined, minMs: undefined, maxMs: undefined })
              }
            >
              <FilterX className="h-3.5 w-3.5" /> Clear
            </Button>
          </div>
        )}
      </div>

      {tab === "overview" ? (
        <OverviewTable
          operations={overview.data?.operations}
          isLoading={overview.isLoading}
          windowMs={windowMs}
          selected={{ service: filters.service, operation: filters.operation }}
          onSelect={(service, operation) =>
            setMany({ service, operation, tab: "traces" })
          }
        />
      ) : (
        <TraceList
          pages={search.data?.pages.map((p) => p.traces)}
          isLoading={search.isLoading}
          hasNextPage={Boolean(search.hasNextPage)}
          isFetchingNextPage={search.isFetchingNextPage}
          fetchNextPage={() => search.fetchNextPage()}
          onSelect={(traceId) => setMany({ trace: traceId })}
          selectedTraceId={selectedTrace ?? undefined}
        />
      )}
    </div>
  );
}
