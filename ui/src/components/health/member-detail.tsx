import Link from "next/link";
import { Map as MapIcon, ListTree, X } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { statusDotClass, statusLabel, statusTone } from "@/lib/health-status";
import type { HealthGroup, HealthMember } from "@/lib/api-types";

function findMember(groups: HealthGroup[], service: string): HealthMember | undefined {
  for (const g of groups) {
    const m = g.members.find((x) => x.service === service);
    if (m) return m;
  }
  return undefined;
}

// Detail for one selected member: base vs effective status, the reason, the
// RED evidence, and the critical-dependency chain — everything the response
// already carries, so no second request. Links out to the map and traces.
export function MemberDetail({
  groups,
  service,
  onClose,
}: {
  groups: HealthGroup[];
  service: string;
  onClose: () => void;
}) {
  const member = findMember(groups, service);
  if (!member) return null;

  const propagated = member.effectiveStatus !== member.baseStatus;

  return (
    <Card className="flex w-full flex-col gap-3 p-4 lg:w-96 lg:shrink-0">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn("h-2.5 w-2.5 rounded-full", statusDotClass(member.effectiveStatus))} aria-hidden />
          <span className="truncate font-mono text-sm font-semibold">{member.service}</span>
          {member.tier && <span className="rounded bg-base-300 px-1 text-[10px] text-base-content/60">{member.tier}</span>}
        </div>
        <button type="button" onClick={onClose} className="text-base-content/50 hover:text-base-content" aria-label="Close">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex items-center gap-2 text-xs">
        <span className="text-base-content/50">Base</span>
        <Badge tone={statusTone(member.baseStatus)}>{statusLabel(member.baseStatus)}</Badge>
        {propagated && (
          <>
            <span className="text-base-content/40">→</span>
            <span className="text-base-content/50">Effective</span>
            <Badge tone={statusTone(member.effectiveStatus)}>{statusLabel(member.effectiveStatus)}</Badge>
          </>
        )}
      </div>

      <p className="text-sm text-base-content/70">{member.reason}</p>

      <dl className="grid grid-cols-3 gap-2 rounded-lg bg-base-100 p-2 text-center text-xs">
        <div>
          <dt className="text-base-content/45">Rate</dt>
          <dd className="tabular-nums">{member.ratePerSec.toFixed(1)}/s</dd>
        </div>
        <div>
          <dt className="text-base-content/45">Errors</dt>
          <dd className="tabular-nums">{(member.errorRate * 100).toFixed(1)}%</dd>
        </div>
        <div>
          <dt className="text-base-content/45">p95</dt>
          <dd className="tabular-nums">{member.p95Ms.toFixed(0)}ms</dd>
        </div>
      </dl>

      {member.dependencies && member.dependencies.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-xs text-base-content/50">Critical dependencies</span>
          <div className="flex flex-wrap gap-1.5">
            {member.dependencies.map((d) => (
              <span
                key={d.service}
                title={`${d.service} — ${statusLabel(d.status)}`}
                className="inline-flex items-center gap-1.5 rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs"
              >
                <span className={cn("h-2 w-2 rounded-full", statusDotClass(d.status))} aria-hidden />
                <span className="font-mono">{d.service}</span>
                {d.tier && <span className="text-[10px] text-base-content/45">{d.tier}</span>}
              </span>
            ))}
          </div>
        </div>
      )}

      <div className="mt-auto flex items-center gap-3 pt-1 text-xs text-primary">
        <Link href="/service-map" className="inline-flex items-center gap-1 hover:underline">
          <MapIcon className="h-3.5 w-3.5" aria-hidden /> Service map
        </Link>
        <Link href={`/traces?service=${encodeURIComponent(member.service)}`} className="inline-flex items-center gap-1 hover:underline">
          <ListTree className="h-3.5 w-3.5" aria-hidden /> Traces
        </Link>
      </div>
    </Card>
  );
}
