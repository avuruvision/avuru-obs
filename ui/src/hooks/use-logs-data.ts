"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { LogsResponse } from "@/lib/api-types";

export interface LogFilters {
  service?: string;
  severity?: string;
  q?: string;
}

export function useLogSearch(time: TimeParams, filters: LogFilters) {
  return useInfiniteQuery({
    queryKey: queryKeys.logs(time, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<LogsResponse>("/api/v1/logs", {
        ...time,
        ...filters,
        limit: 100,
        cursor: pageParam || undefined,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}

export function useTraceLogs(traceId: string | null) {
  return useQuery({
    queryKey: queryKeys.traceLogs(traceId ?? ""),
    queryFn: () => apiGet<LogsResponse>(`/api/v1/traces/${traceId}/logs`),
    enabled: traceId !== null && traceId !== "",
  });
}
