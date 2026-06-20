"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { ServicesResponse } from "@/lib/api-types";

// Nodes-first: the service map is built from the services sending telemetry.
// Call edges (topology) arrive from eBPF flows in a later milestone.
export function useServiceMapData(time: TimeParams) {
  return useQuery({
    queryKey: queryKeys.services(time),
    queryFn: () => apiGet<ServicesResponse>("/api/v1/services", { ...time }),
  });
}
