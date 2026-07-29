"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiGet, apiPost, apiPatch, apiDelete } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { Project, ProjectsResponse } from "@/lib/api-types";

// The selectable project list: {default} ∪ config-defined ∪ UI-managed ∪
// observed in data. Instance-global (no project scoping — it IS the project
// list).
export function useProjects() {
  return useQuery({
    queryKey: queryKeys.projects,
    queryFn: () => apiGet<ProjectsResponse>("/api/v1/projects"),
    staleTime: 30_000,
  });
}

function useInvalidateProjects() {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: queryKeys.projects });
}

export function useCreateProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (input: { id: string; label: string }) =>
      apiPost<Project>("/api/v1/projects", input),
    onSuccess: invalidate,
  });
}

export function useRenameProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    // The hub rename route is PATCH.
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      apiPatch<Project>(`/api/v1/projects/${encodeURIComponent(id)}`, { label }),
    onSuccess: invalidate,
  });
}

export function useDeleteProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (id: string) => apiDelete(`/api/v1/projects/${encodeURIComponent(id)}`),
    onSuccess: invalidate,
  });
}
