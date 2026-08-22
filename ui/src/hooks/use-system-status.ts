"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import { useProject } from "@/lib/project-context";
import type { SystemStatusResponse } from "@/lib/api-types";

// System health is point-in-time (no time range); refetch periodically so the
// Settings → Status tab stays live. The endpoint always answers 200, returning
// ClickHouse "down" rather than failing, so the page renders during an outage.
//
// The response also carries what the SELECTED project holds, so the request
// sends the tenant header (third argument — the second is query params) and the
// key leads with the project: otherwise a project switch would keep showing the
// previous project's footprint from cache.
export function useSystemStatus() {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.systemStatus(project),
    queryFn: () => apiGet<SystemStatusResponse>("/api/v1/system/status", undefined, { project }),
    refetchInterval: 20_000,
  });
}
