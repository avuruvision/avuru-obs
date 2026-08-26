"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { CostNodesResponse, CostWorkloadsResponse } from "@/lib/api-types";

// What each workload reserved against what it used, over the selected window.
// Keyed on the range like the other windowed dashboards: reservations and
// usage are both read over the same window on the hub side, so a client that
// asked for them separately could show a workload's reservation against
// someone else's usage.
export function useCostWorkloads(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.costWorkloads(project, time),
    queryFn: () =>
      apiGet<CostWorkloadsResponse>("/api/v1/cost/workloads", { ...time }, { project }),
  });
}

// Per-node allocatable capacity, what requests have claimed of it, and what is
// in use. "89% requested, 12% used" is one sentence about two problems.
export function useCostNodes(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.costNodes(project, time),
    queryFn: () => apiGet<CostNodesResponse>("/api/v1/cost/nodes", { ...time }, { project }),
  });
}
