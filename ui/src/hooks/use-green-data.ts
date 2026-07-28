"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { GreenSummaryResponse, GreenBudgetsResponse } from "@/lib/api-types";

// Per-service energy (Wh) and carbon (gCO2e) over the selected window: the
// factors in force, attributed/unattributed totals with the coverage ratio,
// and per-service rows with a trend series. Keyed on the time range like the
// other windowed dashboards (infra/health) — it refetches when the range moves,
// not on a poll. The CSRD export is fetched on demand by the export panel, not
// here.
export function useGreenSummary(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.greenSummary(project, time),
    queryFn: () =>
      apiGet<GreenSummaryResponse>("/api/v1/green/summary", { ...time }, { project }),
  });
}

// Month-to-date carbon budget status per configured group. Always the current
// UTC calendar month (the hub ignores time params here), so the key carries no
// range; a gentle poll keeps used/projected/status fresh as energy accrues.
export function useGreenBudgets() {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.greenBudgets(project),
    queryFn: () => apiGet<GreenBudgetsResponse>("/api/v1/green/budgets", undefined, { project }),
    refetchInterval: 60_000,
  });
}
