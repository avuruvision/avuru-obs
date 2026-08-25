import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { statusDotClass, statusLabel, statusTone } from "@/lib/health-status";
import type { HealthGroup } from "@/lib/api-types";
import { MemberChip } from "./member-chip";
import { GroupChecks } from "./group-checks";

const COUNT_ORDER: { key: string; symbol: string; cls: string }[] = [
  { key: "healthy", symbol: "✓", cls: "text-success" },
  { key: "degraded", symbol: "▲", cls: "text-warning" },
  { key: "down", symbol: "✕", cls: "text-error" },
  { key: "idle", symbol: "◔", cls: "text-base-content/50" },
];

// One group's card: status header, rollup reason, a per-status tally, and the
// member chips. Clicking a chip opens the member detail (via the parent).
export function GroupCard({
  group,
  selectedMember,
  onSelectMember,
}: {
  group: HealthGroup;
  selectedMember: string | null;
  onSelectMember: (service: string) => void;
}) {
  return (
    <Card className="flex flex-col gap-2 p-3" data-testid="group-card">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", statusDotClass(group.status))} aria-hidden />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-sm font-semibold">{group.name}</span>
              {group.environment && (
                <span className="rounded bg-primary/15 px-1 text-[10px] text-primary">
                  {group.environment}
                </span>
              )}
              {group.source === "auto" && (
                <span className="rounded bg-base-300 px-1 text-[10px] text-base-content/50">auto</span>
              )}
              {/* Where the tier came from. Worth saying out loud only when the
                  service chose it: an operator reading a T0 lane should know
                  whether ops put it there or the app claimed it. */}
              {group.tierSource === "declared" && (
                <span
                  className="rounded bg-base-300 px-1 text-[10px] text-base-content/50"
                  title="Tier declared by the service itself (avuru.tier). Override it in Settings → Groups."
                >
                  declared
                </span>
              )}
            </div>
            <p className="truncate text-xs text-base-content/55">{group.reason}</p>
          </div>
        </div>
        <Badge tone={statusTone(group.status)}>{statusLabel(group.status)}</Badge>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {group.members.map((m) => (
          <MemberChip
            key={m.service}
            member={m}
            selected={selectedMember === m.service}
            onSelect={onSelectMember}
          />
        ))}
      </div>

      <div className="flex items-center gap-3 text-xs text-base-content/60">
        {COUNT_ORDER.filter((c) => (group.counts?.[c.key] ?? 0) > 0).map((c) => (
          <span key={c.key} className={c.cls}>
            {c.symbol} {group.counts[c.key]}
          </span>
        ))}
        <span className="ml-auto tabular-nums">{group.ratePerSec.toFixed(1)}/s</span>
      </div>

      {/* Renders nothing for a group that declares no checks. */}
      <GroupChecks checks={group.checks ?? []} />
    </Card>
  );
}
