"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { SystemStatusResponse } from "@/lib/api-types";

// System health is point-in-time (no time range); refetch periodically so the
// Settings → Status tab stays live. The endpoint always answers 200, returning
// ClickHouse "down" rather than failing, so the page renders during an outage.
export function useSystemStatus() {
  return useQuery({
    queryKey: queryKeys.systemStatus,
    queryFn: () => apiGet<SystemStatusResponse>("/api/v1/system/status"),
    refetchInterval: 20_000,
  });
}
