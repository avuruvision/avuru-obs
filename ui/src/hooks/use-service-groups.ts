"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  ServiceGroupDef,
  ServiceGroupInput,
  ServiceGroupsResponse,
} from "@/lib/api-types";

// Group definitions are instance-global (no project in key or headers), like
// the service-health config they merge with: groups are an install-level
// grouping of services, not per-tenant data.
export function useServiceGroups() {
  return useQuery({
    queryKey: queryKeys.serviceGroups,
    queryFn: () => apiGet<ServiceGroupsResponse>("/api/v1/service-groups"),
  });
}

// A group edit changes how EVERY health read rolls up, so the health queries
// are dropped alongside the definitions — otherwise the Service Health screen
// keeps showing the old grouping until its next poll.
function useInvalidateServiceGroups() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.serviceGroups });
    void qc.invalidateQueries({ predicate: (q) => q.queryKey[1] === "health" });
  };
}

export function useCreateServiceGroup() {
  const invalidate = useInvalidateServiceGroups();
  return useMutation({
    mutationFn: (input: ServiceGroupInput) =>
      apiPost<ServiceGroupDef>("/api/v1/service-groups", input),
    onSuccess: invalidate,
  });
}

export function useUpdateServiceGroup() {
  const invalidate = useInvalidateServiceGroups();
  return useMutation({
    mutationFn: ({ name, ...input }: ServiceGroupInput) =>
      apiPut<ServiceGroupDef>(`/api/v1/service-groups/${encodeURIComponent(name)}`, input),
    onSuccess: invalidate,
  });
}

export function useDeleteServiceGroup() {
  const invalidate = useInvalidateServiceGroups();
  return useMutation({
    mutationFn: (name: string) =>
      apiDelete(`/api/v1/service-groups/${encodeURIComponent(name)}`),
    onSuccess: invalidate,
  });
}
