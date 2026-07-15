"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { ServicesResponse } from "@/lib/api-types";

// Service inventory: every service seen in the window with its RED stats
// (rate, error rate, latency percentiles). Auxiliary traffic is excluded by
// default; pass includeAux to count health checks/metrics/control-plane too.
export function useServicesData(time: TimeParams, includeAux?: boolean) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.services(project, time, includeAux),
    queryFn: () =>
      apiGet<ServicesResponse>(
        "/api/v1/services",
        {
          ...time,
          includeAux: includeAux ? "true" : undefined,
        },
        { project },
      ),
  });
}
