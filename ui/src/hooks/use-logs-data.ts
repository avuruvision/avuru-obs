"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { LogServicesResponse, LogsResponse, MeshWorkloadLogsResponse } from "@/lib/api-types";

export interface LogFilters {
  services?: string[];
  workloads?: string[];
  source?: string;
  severity?: string;
  q?: string;
  tags?: string; // "key=value,key2=value2" — the same string the traces screen uses
}

// At most four panel requests in flight at once. A finished request hands its
// slot straight to the next waiter rather than freeing it: freeing first would
// let a newcomer take the slot before the waiter wakes, and five would fly.
const PANEL_REQUEST_SLOTS = 4;
const panelWaiters: Array<() => void> = [];
let activePanelRequests = 0;

async function withPanelRequestSlot<T>(run: () => Promise<T>): Promise<T> {
  if (activePanelRequests >= PANEL_REQUEST_SLOTS) {
    await new Promise<void>((resolve) => panelWaiters.push(resolve));
  } else {
    activePanelRequests += 1;
  }
  try {
    return await run();
  } finally {
    const next = panelWaiters.shift();
    if (next) next();
    else activePanelRequests -= 1;
  }
}

// How often a followed panel asks for its newest page.
export const FOLLOW_INTERVAL_MS = 5_000;

// A window of the given length ending now — computed when the request goes
// out, not when the component rendered, so a followed panel keeps up.
function liveWindow(windowMs: number): TimeParams {
  const end = new Date();
  return { start: new Date(end.getTime() - windowMs).toISOString(), end: end.toISOString() };
}

// followWindowMs, when set, turns the search into a tail: one page, the newest
// lines of a window that length ending now, refetched every few seconds. The
// key carries the length rather than the ends, so the ticks land on one cache
// entry instead of minting a new query each time.
export function useLogSearch(time: TimeParams, filters: LogFilters, enabled = true, panel = false, followWindowMs?: number) {
  const { project } = useProject();
  const follow = followWindowMs !== undefined && followWindowMs > 0;
  return useInfiniteQuery({
    queryKey: queryKeys.logs(project, time, { ...filters, ...(follow ? { follow: followWindowMs } : {}) }),
    enabled,
    refetchInterval: follow ? FOLLOW_INTERVAL_MS : false,
    queryFn: ({ pageParam }) => {
      const request = () => apiGet<LogsResponse>(
        "/api/v1/logs",
        {
          ...(follow ? liveWindow(followWindowMs) : time),
          ...filters,
          service: filters.services,
          workload: filters.workloads,
          resolution: pageParam.resolutionToken || undefined,
          services: undefined,
          workloads: undefined,
          limit: 100,
          cursor: pageParam.cursor || undefined,
        },
        { project },
      );
      return panel ? withPanelRequestSlot(request) : request();
    },
    initialPageParam: { cursor: "", resolutionToken: "" },
    // A tail has no older pages: every tick would refetch them all, and the
    // newest hundred is what following means.
    getNextPageParam: (last) => last.nextCursor && !follow
      ? { cursor: last.nextCursor, resolutionToken: last.resolutionToken ?? "" }
      : undefined,
  });
}

export function useLogServices(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.logServices(project, time),
    queryFn: () => apiGet<LogServicesResponse>("/api/v1/logs/services", { ...time }, { project }),
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
