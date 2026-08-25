import Link from "next/link";
import { CircleCheck, CircleX, TriangleAlert } from "lucide-react";
import { formatMs } from "@/lib/format";
import type { HealthCheckState } from "@/lib/api-types";

// The probes answering for a group.
//
// This row is the only thing on the health board that reports on a group with
// no traffic. Everything else here is derived from requests other people made;
// a check is the request WE made, which is why an idle group with a passing
// check reads healthy instead of unknown.
export function GroupChecks({ checks }: { checks: HealthCheckState[] }) {
  if (checks.length === 0) return null;
  return (
    <div
      data-testid="group-checks"
      className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-neutral/60 pt-2 text-xs"
    >
      <span className="text-base-content/45">checks:</span>
      {checks.map((c) => (
        <CheckPill key={c.id} check={c} />
      ))}
    </div>
  );
}

function CheckPill({ check }: { check: HealthCheckState }) {
  // Three states, not two. "Failed once" is deliberately distinguishable from
  // "failing": the first is a restart or a dropped packet and moves nothing,
  // the second is what changed the group's status.
  const { icon: Icon, cls, title } = pillTone(check);
  const body = (
    <span className={`inline-flex items-center gap-1 ${cls}`} title={title}>
      <Icon className="h-3 w-3" aria-hidden />
      <span>{check.id}</span>
      {check.latencyMs !== undefined && check.ok && (
        <span className="tabular-nums text-base-content/45">{formatMs(check.latencyMs)}</span>
      )}
    </span>
  );
  // The join the whole feature exists for: from a failing check straight to the
  // trace of the probe that failed.
  if (check.traceId) {
    return (
      <Link href={`/traces/${check.traceId}`} className="hover:underline">
        {body}
      </Link>
    );
  }
  return body;
}

function pillTone(c: HealthCheckState) {
  if (c.failing) {
    return {
      icon: CircleX,
      cls: "text-error",
      title: c.error || `failing — ${c.consecutiveFailures} probes in a row`,
    };
  }
  if (!c.ok) {
    return {
      icon: TriangleAlert,
      cls: "text-warning",
      title: `${c.error || "last probe failed"} — one failure, not yet a verdict`,
    };
  }
  return {
    icon: CircleCheck,
    cls: "text-success",
    title: c.lastRun ? `passing, last run ${c.lastRun}` : "passing",
  };
}
