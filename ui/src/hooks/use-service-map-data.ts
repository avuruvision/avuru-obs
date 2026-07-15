"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { ServiceMapResponse } from "@/lib/api-types";

// The service map is nodes (services sending telemetry) plus call edges derived
// from trace spans (caller→callee). Auxiliary traffic is excluded by default;
// pass includeAux to show health-check/metrics/control-plane calls. eBPF flows
// will enrich the edges in a later milestone.
export function useServiceMapData(time: TimeParams, includeAux?: boolean) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.serviceMap(project, time, includeAux),
    queryFn: () =>
      apiGet<ServiceMapResponse>(
        "/api/v1/service-map",
        {
          ...time,
          includeAux: includeAux ? "true" : undefined,
        },
        { project },
      ),
  });
}
