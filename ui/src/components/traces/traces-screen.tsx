"use client";

import { useMemo } from "react";
import { Tabs } from "@/components/ui/tabs";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useLocalStorageFlag } from "@/hooks/use-local-storage-flag";
import {
  useHeatmap,
  useServices,
  useTraceOverview,
  useTraceSearch,
  type TraceFilters,
} from "@/hooks/use-traces-data";
import { Heatmap } from "./heatmap";
import { OverviewTable } from "./overview-table";
import { TraceList } from "./trace-list";
import { TraceFilterPanel } from "./trace-filters";
import { TraceWorkspace } from "./trace-workspace";
import { BreakdownPanel } from "./breakdown/breakdown-panel";
import { CLEAR_WORKSPACE_PARAMS } from "./workspace-params";

type Tab = "overview" | "breakdown" | "traces";

// Coroot flow: filter panel + heatmap on top, tabs below; every filter and the
// selected trace live in the URL. The selected trace opens a full-window viewer.
export function TracesScreen() {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();

  const rawTab = get("tab");
  const tab: Tab = rawTab === "traces" || rawTab === "breakdown" ? rawTab : "overview";
  // Breakdown controls are URL state like every other filter, so a shared link
  // reopens the same chart of the same traffic.
  const groupBy = get("groupBy") ?? "service";
  const scope = get("scope") ?? "entry";
  const weight = get("weight") === "time" ? "time" : "count";
  const selectedTrace = get("trace") ?? null;
  const compareTrace = get("compare") ?? null;
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
  const services = useServices(time);
  const [groupByService, setGroupByService] = useLocalStorageFlag("traces:groupByService");

  // Operation names for the Operation filter's type-ahead — reuse the overview
  // data (already fetched), deduped across services. Scoped to the selected
  // service when one is set, otherwise all operations in the window.
  const operationOptions = useMemo(
    () => [...new Set((overview.data?.operations ?? []).map((o) => o.operation))],
    [overview.data],
  );

  return (
    <div className="flex flex-col gap-4">
      <TraceFilterPanel
        filters={filters}
        set={setMany}
        hasFilters={hasFilters}
        traceId={selectedTrace}
        spanId={get("span")}
        services={services.data ?? []}
        servicesLoading={services.isLoading}
        operations={operationOptions}
        operationsLoading={overview.isLoading}
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
          { value: "breakdown", label: "Breakdown" },
          { value: "traces", label: "Traces" },
        ]}
        value={tab}
        // Tabs double as the escape hatch from the trace workspace: selecting
        // one closes the open trace (same params the panel's close button clears).
        onChange={(t) => setMany({ ...CLEAR_WORKSPACE_PARAMS, tab: t })}
      />

      {selectedTrace ? (
        <TraceWorkspace
          traceId={selectedTrace}
          compareId={compareTrace}
          pages={search.data?.pages.map((p) => p.traces)}
          isLoading={search.isLoading}
          hasNextPage={Boolean(search.hasNextPage)}
          isFetchingNextPage={search.isFetchingNextPage}
          fetchNextPage={() => search.fetchNextPage()}
        />
      ) : tab === "breakdown" ? (
        <BreakdownPanel
          time={time}
          filters={filters}
          groupBy={groupBy}
          scope={scope}
          weight={weight}
          onControlChange={setMany}
          onDrill={setMany}
        />
      ) : tab === "overview" ? (
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
        <div className="flex flex-col gap-2">
          <label className="flex cursor-pointer items-center gap-1.5 self-end text-xs text-base-content/70">
            <input
              type="checkbox"
              checked={groupByService}
              onChange={(e) => setGroupByService(e.target.checked)}
              className="accent-primary"
            />
            Group by service
          </label>
          <TraceList
            pages={search.data?.pages.map((p) => p.traces)}
            isLoading={search.isLoading}
            hasNextPage={Boolean(search.hasNextPage)}
            isFetchingNextPage={search.isFetchingNextPage}
            fetchNextPage={() => search.fetchNextPage()}
            onSelect={(traceId) => setMany({ trace: traceId })}
            selectedTraceId={selectedTrace ?? undefined}
            groupByService={groupByService}
          />
        </div>
      )}
    </div>
  );
}
