"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys } from "@/lib/query-keys";
import type {
  AlertsResponse,
  AlertRulesResponse,
  AlertChannel,
  AlertChannelInput,
  AlertChannelsResponse,
} from "@/lib/api-types";

export function useAlerts() {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.alerts(project),
    queryFn: () => apiGet<AlertsResponse>("/api/v1/alerts", undefined, { project }),
    refetchInterval: 15000,
  });
}

export function useAlertRules() {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.alertRules(project),
    queryFn: () => apiGet<AlertRulesResponse>("/api/v1/alerts/rules", undefined, { project }),
  });
}

// Channels are instance-global (no project in key or headers): they are
// delivery endpoints shared by every project; the payload carries the tenant.
export function useAlertChannels() {
  return useQuery({
    queryKey: queryKeys.alertChannels,
    queryFn: () => apiGet<AlertChannelsResponse>("/api/v1/alerts/channels"),
  });
}

function useInvalidateChannels() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.alertChannels });
    // Rules lists render the channel set too — refresh them for every project.
    void qc.invalidateQueries({
      predicate: (q) => q.queryKey[1] === "alerts" && q.queryKey[2] === "rules",
    });
  };
}

export function useCreateChannel() {
  const invalidate = useInvalidateChannels();
  return useMutation({
    mutationFn: (input: AlertChannelInput) =>
      apiPost<AlertChannel>("/api/v1/alerts/channels", input),
    onSuccess: invalidate,
  });
}

export function useUpdateChannel() {
  const invalidate = useInvalidateChannels();
  return useMutation({
    mutationFn: ({ name, ...input }: AlertChannelInput) =>
      apiPut<AlertChannel>(`/api/v1/alerts/channels/${encodeURIComponent(name)}`, input),
    onSuccess: invalidate,
  });
}

export function useDeleteChannel() {
  const invalidate = useInvalidateChannels();
  return useMutation({
    mutationFn: (name: string) =>
      apiDelete(`/api/v1/alerts/channels/${encodeURIComponent(name)}`),
    onSuccess: invalidate,
  });
}

export function useTestChannel() {
  return useMutation({
    mutationFn: (name: string) =>
      apiPost<{ ok: boolean }>(`/api/v1/alerts/channels/${encodeURIComponent(name)}/test`, {}),
  });
}
