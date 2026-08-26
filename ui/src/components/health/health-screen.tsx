"use client";

import { useMemo } from "react";
import Link from "next/link";
import { Activity, SlidersHorizontal, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/cn";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useHealthGroups } from "@/hooks/use-health-data";
import { useAuth } from "@/hooks/use-auth";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { statusDotClass, statusLabel } from "@/lib/health-status";
import type { HealthGroup } from "@/lib/api-types";
import { TierLane } from "./tier-lane";
import { MemberDetail } from "./member-detail";

const TIER_LANES: { tier: string; title: string; subtitle?: string }[] = [
  { tier: "T0", title: "Tier 0", subtitle: "Critical" },
  { tier: "T1", title: "Tier 1" },
  { tier: "T2", title: "Tier 2" },
  { tier: "T3", title: "Tier 3", subtitle: "Degradable" },
];

// Board of consolidated group health, laid out in tier lanes. Members roll up
// per-service RED health (with critical-dependency propagation); a click opens
// the member detail via ?member= (static-export-safe, no dynamic route).
export function HealthScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const selected = get("member") ?? null;

  const { data, isLoading } = useHealthGroups(time, includeAux);
  // Grouping is authored in an admin-only editor. Pointing a read-only
  // viewer at it sends them to a tab that is not theirs — the board still
  // reads the same, it just stops offering a door they cannot open.
  const { canAdminister } = useAuth();
  const groups = useMemo(() => data?.groups ?? [], [data]);

  const lanes = useMemo(() => {
    const known = new Set(TIER_LANES.map((l) => l.tier));
    const byTier = (t: string) => groups.filter((g) => g.tier === t);
    const other = groups.filter((g) => !known.has(g.tier));
    return { byTier, other };
  }, [groups]);

  if (isLoading) return <CenteredSpinner />;

  if (!groups.length) {
    return (
      <EmptyState icon={Activity} title="No service health yet">
        Health is derived from your traces (RED) — services appear here as soon
        as telemetry flows.
        {canAdminister && (
          <>
            {" "}
            Then say which ones matter in{" "}
            <Link href="/settings?tab=groups" className="text-primary hover:underline">
              Settings → Groups
            </Link>{" "}
            (e.g. <code className="rounded bg-base-300 px-1">auth-service → T0</code>).
          </>
        )}
      </EmptyState>
    );
  }

  const selectMember = (service: string) =>
    setMany({ member: service === selected ? undefined : service });

  return (
    <div className="flex flex-col gap-4 lg:flex-row">
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {/* Declarations fail soft — an unusable avuru.tier falls back to the
            default rather than breaking the board. That is the right behaviour
            (application telemetry gets no operator review) and it is also how a
            team never finds out their declaration was ignored. So it is said
            here, once, above the lanes it affected. */}
        {data?.warnings?.length ? (
          <div
            className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 p-2.5 text-xs"
            data-testid="health-warnings"
          >
            <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" aria-hidden />
            <div className="flex flex-col gap-0.5">
              <span className="font-medium">
                {data.warnings.length === 1
                  ? "A service declared something the hub could not use"
                  : `${data.warnings.length} services declared something the hub could not use`}
              </span>
              {data.warnings.map((w) => (
                <span key={w} className="text-base-content/70">
                  {w}
                </span>
              ))}
            </div>
          </div>
        ) : null}

        <div className="flex items-center gap-2 text-sm">
          {data && (
            <span className={cn("h-3 w-3 rounded-full", statusDotClass(data.overall))} aria-hidden />
          )}
          <span className="font-semibold">
            Overall: {data ? statusLabel(data.overall) : "—"}
          </span>
          <span className="text-base-content/50">· {groups.length} groups</span>
          <label className="ml-auto flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
            <input
              type="checkbox"
              checked={includeAux}
              onChange={(e) => setMany({ includeAux: e.target.checked ? "true" : undefined })}
              className="accent-primary"
            />
            Show auxiliary requests
          </label>
          {/* The lanes below are only as useful as the grouping behind them,
              and that is now editable — so the way to change it is one click
              from the board it changes. */}
          {canAdminister && (
            <Link
              href="/settings?tab=groups"
              className="flex items-center gap-1 text-xs text-base-content/70 hover:text-base-content"
              data-testid="manage-groups-link"
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              Manage groups
            </Link>
          )}
        </div>

        {TIER_LANES.map((lane) => {
          const laneGroups = lanes.byTier(lane.tier);
          if (!laneGroups.length) return null;
          return (
            <TierLane
              key={lane.tier}
              title={lane.title}
              subtitle={lane.subtitle}
              tier={lane.tier}
              groups={laneGroups}
              selectedMember={selected}
              onSelectMember={selectMember}
            />
          );
        })}

        {lanes.other.length > 0 && (
          <TierLane
            title="Unassigned"
            subtitle="no tier configured"
            groups={lanes.other as HealthGroup[]}
            selectedMember={selected}
            onSelectMember={selectMember}
          />
        )}
      </div>

      {selected && (
        <MemberDetail
          groups={groups}
          service={selected}
          onClose={() => setMany({ member: undefined })}
        />
      )}
    </div>
  );
}
