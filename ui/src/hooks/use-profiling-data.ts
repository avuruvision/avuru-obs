"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { FlamegraphResponse, ProfiledServicesResponse } from "@/lib/api-types";

// Services with CPU profiling samples in the window, busiest first.
export function useProfiledServices(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.profiledServices(project, time),
    queryFn: () =>
      apiGet<ProfiledServicesResponse>("/api/v1/profiles/services", { ...time }, { project }),
  });
}

// One service's aggregated flame graph over the window.
export function useFlamegraph(time: TimeParams, service: string) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.flamegraph(project, time, service),
    queryFn: () =>
      apiGet<FlamegraphResponse>("/api/v1/profiles/flamegraph", { ...time, service }, { project }),
    enabled: service !== "",
  });
}
