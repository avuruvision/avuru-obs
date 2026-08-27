"use client";

import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  BreakdownResponse,
  HeatmapResponse,
  OverviewResponse,
  ServicesResponse,
  SpanLookupResponse,
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

// Distinct service names in the window, for the Service filter's type-ahead.
// Keyed by time only (matches queryKeys.services); the default (aux-excluded)
// list is the right set of names to filter by.
export function useServices(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.services(project, time),
    queryFn: () => apiGet<ServicesResponse>("/api/v1/services", { ...time }, { project }),
    select: (data) => data.services.map((s) => s.name),
  });
}

export function useTraceOverview(time: TimeParams, service?: string, includeAux?: boolean) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.traceOverview(project, time, service, includeAux),
    queryFn: () =>
      apiGet<OverviewResponse>(
        "/api/v1/traces/overview",
        {
          ...time,
          service,
          includeAux: aux(includeAux),
        },
        { project },
      ),
  });
}

export function useHeatmap(time: TimeParams, filters: TraceFilters) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.heatmap(project, time, {
      service: filters.service,
      operation: filters.operation,
      tags: filters.tags,
      includeAux: filters.includeAux,
    }),
    queryFn: () =>
      apiGet<HeatmapResponse>(
        "/api/v1/traces/heatmap",
        {
          ...time,
          service: filters.service,
          operation: filters.operation,
          tags: filters.tags,
          includeAux: aux(filters.includeAux),
          timeBuckets: 60,
          durationBuckets: 24,
        },
        { project },
      ),
  });
}

// One grouped aggregate over spans, for the part-of-whole views.
//
// It takes the SAME TraceFilters as the list and the heatmap, so the chart and
// the rows under it can never describe different traffic — the filter panel
// drives all three from one piece of URL state.
export function useTraceBreakdown(
  time: TimeParams,
  filters: TraceFilters,
  groupBy: string,
  scope: string,
  limit = 20,
) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.breakdown(project, time, { ...filters, groupBy, scope, limit }),
    queryFn: () =>
      apiGet<BreakdownResponse>(
        "/api/v1/traces/breakdown",
        {
          ...time,
          groupBy,
          scope,
          limit,
          service: filters.service,
          operation: filters.operation,
          status: filters.status,
          tags: filters.tags,
          minDurationMs: filters.minDurationMs,
          maxDurationMs: filters.maxDurationMs,
          includeAux: aux(filters.includeAux),
        },
        { project },
      ),
  });
}

export function useTraceSearch(time: TimeParams, filters: TraceFilters) {
  const { project } = useProject();
  return useInfiniteQuery({
    queryKey: queryKeys.traces(project, time, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<TracesResponse>(
        "/api/v1/traces",
        {
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
        },
        { project },
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}

// Resolves a span id to its containing trace (the "paste a span id" search).
// A mutation, not a query: fired on Enter, and a miss (404) is user feedback,
// not a retryable fetch.
export function useResolveSpan() {
  const { project } = useProject();
  return useMutation({
    mutationFn: (spanId: string) =>
      apiGet<SpanLookupResponse>(`/api/v1/spans/${spanId}`, undefined, { project }),
  });
}

export function useTrace(traceId: string | null) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.trace(project, traceId ?? ""),
    queryFn: () => apiGet<TraceResponse>(`/api/v1/traces/${traceId}`, undefined, { project }),
    enabled: traceId !== null && traceId !== "",
  });
}
