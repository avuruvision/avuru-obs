"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys } from "@/lib/query-keys";
import type { AlertsResponse, AlertRulesResponse } from "@/lib/api-types";

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
