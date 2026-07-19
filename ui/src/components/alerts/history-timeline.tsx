import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/cn";
import { formatAgo } from "@/lib/format";
import { statusDotClass } from "@/lib/health-status";
import type { AlertHistoryEntry } from "@/lib/api-types";

// Recent fire/resolve events, newest first.
export function HistoryTimeline({ history }: { history: AlertHistoryEntry[] }) {
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Recent activity</CardTitle>
      </CardHeader>
      {history.length === 0 ? (
        <p className="px-4 pb-4 text-sm text-base-content/55">No alerts yet.</p>
      ) : (
        <ul className="divide-y divide-neutral">
          {history.map((h, i) => (
            <li key={`${h.rule}:${h.target}:${h.firedAt}:${i}`} className="flex items-center gap-2.5 px-4 py-2 text-sm">
              <span
                className={cn(
                  "h-2 w-2 shrink-0 rounded-full",
                  h.kind === "resolved" ? "bg-success" : statusDotClass(h.status),
                )}
                aria-hidden
              />
              <span className="text-xs font-medium uppercase text-base-content/50">
                {h.kind}
              </span>
              <span className="truncate font-mono">{h.target}</span>
              <span className="ml-auto shrink-0 text-xs text-base-content/45">{formatAgo(h.firedAt)}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
