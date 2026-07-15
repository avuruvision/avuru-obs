"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys } from "@/lib/query-keys";
import type { AgentsResponse } from "@/lib/api-types";

// Sensor inventory derived from telemetry freshness per node (Coroot-style
// "N nodes reporting"). Read-only until the OpAMP control plane (v0.2).
export function useAgents(windowSec = 600) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.agents(project, windowSec),
    queryFn: () => apiGet<AgentsResponse>("/api/v1/agents", { windowSec }, { project }),
    refetchInterval: 30_000,
  });
}
