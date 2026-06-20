"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  HeatmapResponse,
  OverviewResponse,
  TraceResponse,
  TracesResponse,
} from "@/lib/api-types";

export interface TraceFilters {
  service?: string;
  operation?: string;
  status?: string;
  minDurationMs?: number;
  maxDurationMs?: number;
}

export function useTraceOverview(time: TimeParams, service?: string) {
  return useQuery({
    queryKey: queryKeys.traceOverview(time, service),
    queryFn: () =>
      apiGet<OverviewResponse>("/api/v1/traces/overview", { ...time, service }),
  });
}

export function useHeatmap(time: TimeParams, filters: TraceFilters) {
  return useQuery({
    queryKey: queryKeys.heatmap(time, filters.service, filters.operation),
    queryFn: () =>
      apiGet<HeatmapResponse>("/api/v1/traces/heatmap", {
        ...time,
        service: filters.service,
        operation: filters.operation,
        timeBuckets: 60,
        durationBuckets: 24,
      }),
  });
}

export function useTraceSearch(time: TimeParams, filters: TraceFilters) {
  return useInfiniteQuery({
    queryKey: queryKeys.traces(time, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<TracesResponse>("/api/v1/traces", {
        ...time,
        ...filters,
        limit: 50,
        cursor: pageParam || undefined,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}

export function useTrace(traceId: string | null) {
  return useQuery({
    queryKey: queryKeys.trace(traceId ?? ""),
    queryFn: () => apiGet<TraceResponse>(`/api/v1/traces/${traceId}`),
    enabled: traceId !== null && traceId !== "",
  });
}
