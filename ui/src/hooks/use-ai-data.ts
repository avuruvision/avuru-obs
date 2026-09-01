"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  AICallersResponse,
  AIModelsResponse,
  AIToolsResponse,
} from "@/lib/api-types";

// Per-model usage over the window. The response carries the window's totals
// alongside the rows — computed before the limit on the hub side — so the
// summary above the table and the table itself are one read and cannot
// disagree about how many calls the window held.
export function useAIModels(time: TimeParams, service?: string) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.aiModels(project, time, service),
    queryFn: () =>
      apiGet<AIModelsResponse>(
        "/api/v1/ai/models",
        { ...time, service },
        { project },
      ),
  });
}

// The same calls, grouped by who made them: spend with an owner.
export function useAICallers(time: TimeParams, service?: string) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.aiCallers(project, time, service),
    queryFn: () =>
      apiGet<AICallersResponse>(
        "/api/v1/ai/callers",
        { ...time, service },
        { project },
      ),
  });
}

// The tools an agent turn ran. A different population from the two above — tool
// executions, not model calls — which is exactly the distinction the module got
// wrong before operation classes existed.
export function useAITools(time: TimeParams, service?: string) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.aiTools(project, time, service),
    queryFn: () =>
      apiGet<AIToolsResponse>(
        "/api/v1/ai/tools",
        { ...time, service },
        { project },
      ),
  });
}
