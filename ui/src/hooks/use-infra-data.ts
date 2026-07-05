"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { NodesResponse, PodsResponse } from "@/lib/api-types";

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
