"use client";

import { useTimeRange } from "@/hooks/use-time-range";
import { useHealthGroups } from "@/hooks/use-health-data";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { CenteredSpinner } from "@/components/ui/spinner";
import { SummaryCards } from "./summary-cards";
import type { ServiceStats } from "@/lib/api-types";

// Band 1's data seam. The health query lives in a child that only mounts when
// the service-health module is active — hooks cannot be called conditionally,
// and firing /api/v1/health/groups at an install that doesn't run the module
// would 404 on every dashboard load.
//
// `services` is threaded in from the screen's existing service-map query rather
// than fetched again: /api/v1/service-map already returns the full ServiceStats
// list this band needs for its fallback, so the Dashboard costs no extra call.

function GroupedSummary({ services }: { services: ServiceStats[] }) {
  const { time } = useTimeRange();
  // Auxiliary traffic stays excluded, matching the Health screen's default —
  // the two screens must not disagree about what a group's rate is.
  const { data, isLoading } = useHealthGroups(time, false);

  if (isLoading) return <CenteredSpinner />;

  // Zero groups means the module is on but has nothing to consolidate yet.
  // Showing services beats showing an empty grid.
  const groups = data?.groups ?? [];
  if (!groups.length) return <SummaryCards services={services} />;

  return <SummaryCards groups={groups} services={services} />;
}

export function SummaryBand({ services }: { services: ServiceStats[] }) {
  const healthEnabled = useModuleEnabled("service-health");
  return healthEnabled ? (
    <GroupedSummary services={services} />
  ) : (
    <SummaryCards services={services} />
  );
}
