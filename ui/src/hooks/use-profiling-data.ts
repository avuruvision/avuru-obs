"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { FlamegraphResponse, ProfiledServicesResponse } from "@/lib/api-types";

// Services with CPU profiling samples in the window, busiest first.
export function useProfiledServices(time: TimeParams) {
  return useQuery({
    queryKey: queryKeys.profiledServices(time),
    queryFn: () => apiGet<ProfiledServicesResponse>("/api/v1/profiles/services", { ...time }),
  });
}

// One service's aggregated flame graph over the window.
export function useFlamegraph(time: TimeParams, service: string) {
  return useQuery({
    queryKey: queryKeys.flamegraph(time, service),
    queryFn: () =>
      apiGet<FlamegraphResponse>("/api/v1/profiles/flamegraph", { ...time, service }),
    enabled: service !== "",
  });
}
