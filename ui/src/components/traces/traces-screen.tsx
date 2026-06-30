"use client";

import { useMemo } from "react";
import { Tabs } from "@/components/ui/tabs";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import {
  useHeatmap,
  useTraceOverview,
  useTraceSearch,
  type TraceFilters,
} from "@/hooks/use-traces-data";
import { Heatmap } from "./heatmap";
import { OverviewTable } from "./overview-table";
import { TraceList } from "./trace-list";
import { TraceFilterPanel } from "./trace-filters";
import { TraceViewer } from "./trace-viewer";

type Tab = "overview" | "traces";

// Coroot flow: filter panel + heatmap on top, tabs below; every filter and the
// selected trace live in the URL. The selected trace opens a full-window viewer.
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
      order: get("order"),
      tags: get("tags"),
      minDurationMs: get("minMs") ? Number(get("minMs")) : undefined,
      maxDurationMs: get("maxMs") ? Number(get("maxMs")) : undefined,
      includeAux: get("includeAux") === "true",
    }),
    [get],
  );
  const hasFilters = Boolean(
    filters.service ||
      filters.operation ||
      filters.status ||
      filters.order ||
      filters.tags ||
      filters.minDurationMs ||
      filters.maxDurationMs ||
      filters.includeAux,
  );

  const overview = useTraceOverview(time, filters.service, filters.includeAux);
  const heatmap = useHeatmap(time, filters);
  const search = useTraceSearch(time, filters);

  return (
    <div className="flex flex-col gap-4">
      <TraceFilterPanel
        filters={filters}
        set={setMany}
        hasFilters={hasFilters}
        onClear={() =>
          setMany({
            service: undefined,
            operation: undefined,
            status: undefined,
            order: undefined,
            tags: undefined,
            minMs: undefined,
            maxMs: undefined,
            includeAux: undefined,
          })
        }
      />

      <Heatmap
        data={heatmap.data}
        isLoading={heatmap.isLoading}
        onSelectBand={(minMs, maxMs) =>
          setMany({ minMs: String(minMs), maxMs: String(maxMs), tab: "traces" })
        }
      />

      <Tabs<Tab>
        items={[
          { value: "overview", label: "Overview" },
          { value: "traces", label: "Traces" },
        ]}
        value={tab}
        onChange={(t) => setMany({ tab: t })}
      />

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

      {selectedTrace && (
        <TraceViewer traceId={selectedTrace} onClose={() => setMany({ trace: undefined, view: undefined })} />
      )}
    </div>
  );
}
