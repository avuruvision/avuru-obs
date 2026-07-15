"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { LogsResponse } from "@/lib/api-types";

export interface LogFilters {
  service?: string;
  severity?: string;
  q?: string;
}

export function useLogSearch(time: TimeParams, filters: LogFilters) {
  const { project } = useProject();
  return useInfiniteQuery({
    queryKey: queryKeys.logs(project, time, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<LogsResponse>(
        "/api/v1/logs",
        {
          ...time,
          ...filters,
          limit: 100,
          cursor: pageParam || undefined,
        },
        { project },
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}

export function useTraceLogs(traceId: string | null) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.traceLogs(project, traceId ?? ""),
    queryFn: () => apiGet<LogsResponse>(`/api/v1/traces/${traceId}/logs`, undefined, { project }),
    enabled: traceId !== null && traceId !== "",
  });
}
