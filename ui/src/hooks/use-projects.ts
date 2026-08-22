"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  Project,
  ProjectsResponse,
  CreateProjectRequest,
  UpdateProjectRequest,
} from "@/lib/api-types";

// The selectable project list: {default} ∪ config-defined ∪ db-managed ∪
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
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.projects });
  };
}

// Admin project CRUD. Mutations invalidate the instance-global projects list so
// the switcher and General tab reflect the change.
export function useCreateProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: (input: CreateProjectRequest) => apiPost<Project>("/api/v1/projects", input),
    onSuccess: invalidate,
  });
}

// Edits a db project: label, members, or both (the hub treats an omitted field
// as "keep"). Named for the endpoint rather than the label form now that
// membership shares it.
export function useUpdateProject() {
  const invalidate = useInvalidateProjects();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateProjectRequest }) =>
      apiPut<Project>(`/api/v1/projects/${encodeURIComponent(id)}`, input),
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
