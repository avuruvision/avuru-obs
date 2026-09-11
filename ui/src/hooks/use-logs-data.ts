"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { LogsResponse, MeshWorkloadLogsResponse } from "@/lib/api-types";

export interface LogFilters {
  service?: string;
  severity?: string;
  q?: string;
  tags?: string; // "key=value,key2=value2" — the same string the traces screen uses
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

export interface ServiceLogFilters {
  q?: string;
  severity?: string;
  // Comma list of app, ztunnel, waypoint; absent means all three.
  source?: string;
}

// A page of a service's logs, and the workload the hub resolved for it. The
// workload rides the page param rather than component state: every page after
// the first echoes back what the first one resolved, so the hub does not
// re-resolve — and, more importantly, a snapshot refresh cannot change the
// source set halfway down a scroll, which the cursor could not survive.
interface ServiceLogPageParam {
  cursor: string;
  workload?: string;
  namespace?: string;
}

// One service's logs, composed the way a workload's are: the app's own lines
// plus, when the service could be tied to a workload, what the proxies wrote
// about it. Same page shape as the logs screen's, so the same table renders it.
export function useServiceLogs(time: TimeParams, service: string, filters: ServiceLogFilters) {
  const { project } = useProject();
  return useInfiniteQuery({
    enabled: !!service,
    queryKey: queryKeys.serviceLogs(project, time, service, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<MeshWorkloadLogsResponse>(
        `/api/v1/services/${encodeURIComponent(service)}/logs`,
        {
          ...time,
          ...filters,
          workload: pageParam.workload,
          namespace: pageParam.namespace,
          limit: 100,
          cursor: pageParam.cursor || undefined,
        },
        { project },
      ),
    initialPageParam: { cursor: "" } as ServiceLogPageParam,
    getNextPageParam: (last): ServiceLogPageParam | undefined =>
      last.nextCursor
        ? {
            cursor: last.nextCursor,
            workload: last.sources.workload,
            namespace: last.sources.namespace,
          }
        : undefined,
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
