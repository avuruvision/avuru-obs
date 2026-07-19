import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { formatAgo } from "@/lib/format";
import { statusDotClass } from "@/lib/health-status";
import type { FiringAlert } from "@/lib/api-types";

// Currently-firing alerts. Empty is the good state, so it says so plainly.
export function FiringList({ firing }: { firing: FiringAlert[] }) {
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Firing</CardTitle>
        <Badge tone={firing.length ? "error" : "success"}>
          {firing.length ? `${firing.length} firing` : "all clear"}
        </Badge>
      </CardHeader>
      {firing.length === 0 ? (
        <p className="px-4 pb-4 text-sm text-base-content/55">
          Nothing is firing right now.
        </p>
      ) : (
        <ul className="divide-y divide-neutral">
          {firing.map((a) => (
            <li key={`${a.rule}:${a.target}`} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", statusDotClass(a.status))} aria-hidden />
              <span className="font-mono">{a.target}</span>
              <span className="text-base-content/50">· rule {a.rule}</span>
              <span className="ml-auto text-xs text-base-content/50">since {formatAgo(a.since)}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
