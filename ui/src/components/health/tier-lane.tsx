import { cn } from "@/lib/cn";
import { statusDotClass, statusLabel } from "@/lib/health-status";
import type { HealthGroup, HealthStatus } from "@/lib/api-types";
import { GroupCard } from "./group-card";

// worst-of the lane's group statuses, for the lane header dot.
function laneStatus(groups: HealthGroup[]): HealthStatus {
  const rank: Record<string, number> = { healthy: 1, degraded: 2, down: 3 };
  let worst: HealthStatus = "idle";
  for (const g of groups) {
    if ((rank[g.status] ?? 0) > (rank[worst] ?? 0)) worst = g.status;
  }
  return worst;
}

// One tier lane (T0/T1/T2/T3/Unassigned): a header with the lane's worst status
// and a responsive grid of its group cards.
export function TierLane({
  title,
  subtitle,
  tier,
  groups,
  selectedMember,
  onSelectMember,
}: {
  title: string;
  subtitle?: string;
  tier?: string;
  groups: HealthGroup[];
  selectedMember: string | null;
  onSelectMember: (service: string) => void;
}) {
  const status = laneStatus(groups);
  return (
    <section className="flex flex-col gap-2" data-testid={`tier-lane-${tier ?? "other"}`}>
      <div className="flex items-center gap-2 border-b border-neutral pb-1.5">
        <span className={cn("h-2.5 w-2.5 rounded-full", statusDotClass(status))} aria-hidden />
        <h2 className="text-sm font-semibold">{title}</h2>
        {subtitle && <span className="text-xs text-base-content/45">{subtitle}</span>}
        <span className="ml-auto text-xs text-base-content/45">{statusLabel(status)}</span>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {groups.map((g) => (
          <GroupCard
            // Identity is (domain, environment) now, so the name alone is not
            // unique: one domain declared in two environments is two cards.
            key={`${g.name}\u0000${g.environment ?? ""}`}
            group={g}
            selectedMember={selectedMember}
            onSelectMember={onSelectMember}
          />
        ))}
      </div>
    </section>
  );
}
