import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import type { AlertRule, AlertChannel } from "@/lib/api-types";

function targets(r: AlertRule): string {
  const parts: string[] = [];
  if (r.groups?.length) parts.push(`groups: ${r.groups.join(", ")}`);
  if (r.services?.length) parts.push(`services: ${r.services.join(", ")}`);
  if (r.tiers?.length) parts.push(`tiers: ${r.tiers.join(", ")}`);
  return parts.join(" · ") || "—";
}

// Read-only view of the configured rules and channels (edited in the ConfigMap,
// not here). Channel secrets are never sent by the API.
export function RulesList({ rules, channels }: { rules: AlertRule[]; channels: AlertChannel[] }) {
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Configured rules</CardTitle>
        <span className="text-xs text-base-content/45">edited in config</span>
      </CardHeader>
      {rules.length === 0 ? (
        <p className="px-4 pb-4 text-sm text-base-content/55">No rules configured.</p>
      ) : (
        <ul className="divide-y divide-neutral">
          {rules.map((r) => (
            <li key={r.name} className="flex flex-col gap-0.5 px-4 py-2.5 text-sm">
              <div className="flex items-center gap-2">
                <span className="font-medium">{r.name}</span>
                <Badge tone="neutral">when {r.when}</Badge>
                {r.forSec > 0 && <span className="text-xs text-base-content/50">for {r.forSec}s</span>}
                <span className="ml-auto text-xs text-base-content/50">→ {r.channel}</span>
              </div>
              <span className="text-xs text-base-content/55">{targets(r)}</span>
            </li>
          ))}
        </ul>
      )}
      {channels.length > 0 && (
        <div className="border-t border-neutral px-4 py-2 text-xs text-base-content/55">
          Channels:{" "}
          {channels.map((c, i) => (
            <span key={c.name}>
              {i > 0 && ", "}
              <span className="font-mono">{c.name}</span>
              {c.hasAuth && <span className="text-base-content/40"> (signed)</span>}
            </span>
          ))}
        </div>
      )}
    </Card>
  );
}
