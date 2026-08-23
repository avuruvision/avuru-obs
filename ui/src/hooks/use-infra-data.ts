"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { NodesResponse, PodsResponse, ZonesResponse } from "@/lib/api-types";

// Node utilization (kubeletstats via the sensor): latest CPU/memory/network
// per node plus short series for sparklines.
export function useNodesData(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.infraNodes(project, time),
    queryFn: () => apiGet<NodesResponse>("/api/v1/infra/nodes", { ...time }, { project }),
  });
}

// Pods, busiest first — optionally only those scheduled on one node.
export function usePodsData(time: TimeParams, node?: string) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.infraPods(project, time, node),
    queryFn: () =>
      apiGet<PodsResponse>(
        "/api/v1/infra/pods",
        { ...time, node: node || undefined },
        { project },
      ),
  });
}

// Cross-zone byte volume per zone pair. Opt-in at the sensor, so an empty list
// is the normal answer and the caller is expected to render nothing for it.
export function useZoneTraffic(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.zoneTraffic(project, time),
    queryFn: () => apiGet<ZonesResponse>("/api/v1/network/zones", { ...time }, { project }),
  });
}
