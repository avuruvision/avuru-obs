"use client";

import { useAlerts } from "@/hooks/use-alerts-data";
import { FiringList } from "@/components/alerts/firing-list";

// Band 2, right column. Same seam rule as SummaryBand: this only mounts when
// the alerting module is active, so /api/v1/alerts is never called on an
// install that doesn't run it. FiringList is reused as-is — "all clear" is
// already its empty state, and the Dashboard wants exactly that answer.
export function AlertsCard() {
  const { data } = useAlerts();
  return <FiringList firing={data?.firing ?? []} />;
}
