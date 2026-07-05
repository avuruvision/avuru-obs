"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { RedResponse } from "@/lib/api-types";

// Bucketed RED series per service (entry spans). Empty service = the busiest
// services of the window, backend-side.
export function useRedData(time: TimeParams, service?: string, includeAux?: boolean) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.red(project, time, service, includeAux),
    queryFn: () =>
      apiGet<RedResponse>(
        "/api/v1/metrics/red",
        {
          ...time,
          service: service || undefined,
          includeAux: includeAux ? "true" : undefined,
        },
        { project },
      ),
  });
}
