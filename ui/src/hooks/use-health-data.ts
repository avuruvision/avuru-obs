"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { HealthGroupsResponse } from "@/lib/api-types";

export function useHealthGroups(time: TimeParams, includeAux: boolean) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.healthGroups(project, time, includeAux),
    queryFn: () =>
      apiGet<HealthGroupsResponse>(
        "/api/v1/health/groups",
        { ...time, includeAux: includeAux ? "true" : undefined },
        { project },
      ),
  });
}
