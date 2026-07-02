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
  order?: string; // "newest" (default) | "oldest" | "slowest"
  tags?: string; // "key=value,key2=value2"
  minDurationMs?: number;
  maxDurationMs?: number;
  includeAux?: boolean; // show health-check/metrics/control-plane traffic
}

// The backend excludes auxiliary traffic by default; send includeAux=true only
// when the user opts in.
const aux = (includeAux?: boolean) => (includeAux ? "true" : undefined);

export function useTraceOverview(time: TimeParams, service?: string, includeAux?: boolean) {
  return useQuery({
    queryKey: queryKeys.traceOverview(time, service, includeAux),
    queryFn: () =>
      apiGet<OverviewResponse>("/api/v1/traces/overview", {
        ...time,
        service,
        includeAux: aux(includeAux),
      }),
  });
}

export function useHeatmap(time: TimeParams, filters: TraceFilters) {
  return useQuery({
    queryKey: queryKeys.heatmap(time, {
      service: filters.service,
      operation: filters.operation,
      tags: filters.tags,
      includeAux: filters.includeAux,
    }),
    queryFn: () =>
      apiGet<HeatmapResponse>("/api/v1/traces/heatmap", {
        ...time,
        service: filters.service,
        operation: filters.operation,
        tags: filters.tags,
        includeAux: aux(filters.includeAux),
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
        service: filters.service,
        operation: filters.operation,
        status: filters.status,
        order: filters.order,
        tags: filters.tags,
        minDurationMs: filters.minDurationMs,
        maxDurationMs: filters.maxDurationMs,
        includeAux: aux(filters.includeAux),
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
