import { Badge } from "@/components/ui/badge";

// measured (RAPL/Kepler, hardware-sourced) vs estimated (tdp-estimator,
// modeled from utilization, ±30-50% typical error) — never rendered as one
// blended number; see design/2026-07-28-green-tdp-estimation.md.
export function QualityBadge({ estimatedShare }: { estimatedShare: number }) {
  if (estimatedShare <= 0) return null;
  const pct = Math.round(estimatedShare * 100);
  return (
    <Badge
      tone="warning"
      title="Modeled from CPU utilization, not measured from hardware — typical error ±30-50%"
    >
      {pct >= 100 ? "estimated" : `${pct}% estimated`}
    </Badge>
  );
}
