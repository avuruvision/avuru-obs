"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { ProjectsResponse } from "@/lib/api-types";

// The selectable project list: {default} ∪ config-defined ∪ observed in data.
// Instance-global (no project scoping — it IS the project list).
export function useProjects() {
  return useQuery({
    queryKey: queryKeys.projects,
    queryFn: () => apiGet<ProjectsResponse>("/api/v1/projects"),
    staleTime: 30_000,
  });
}
