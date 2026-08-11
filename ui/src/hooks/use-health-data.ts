"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { HealthGroupsResponse } from "@/lib/api-types";

// `enabled` gates the request for callers that render on installs WITHOUT the
// service-health module (the service map). The endpoint 404s there, so the
// query must never fire rather than fail loudly on every map load.
export function useHealthGroups(time: TimeParams, includeAux: boolean, enabled = true) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.healthGroups(project, time, includeAux),
    queryFn: () =>
      apiGet<HealthGroupsResponse>(
        "/api/v1/health/groups",
        { ...time, includeAux: includeAux ? "true" : undefined },
        { project },
      ),
    enabled,
  });
}
