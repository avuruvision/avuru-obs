import { formatAgo } from "@/lib/format";
import type { ErrorIssue } from "@/lib/api-types";
import { Badge } from "@/components/ui/badge";
import { IssueStatusBadge } from "./issue-status-badge";

// The issue table. Each row opens the detail panel via the parent's onSelect
// (URL-driven ?issue=<fingerprint>), keeping the page static-export safe.
export function IssueList({
  issues,
  selected,
  onSelect,
}: {
  issues: ErrorIssue[];
  selected: string | null;
  onSelect: (fingerprint: string) => void;
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-neutral">
      <table className="w-full text-sm">
        <thead className="bg-base-200 text-xs uppercase tracking-wider text-base-content/50">
          <tr>
            <th className="px-3 py-2 text-left font-semibold">Error</th>
            <th className="px-3 py-2 text-left font-semibold">Service</th>
            <th className="px-3 py-2 text-right font-semibold">Events</th>
            <th className="px-3 py-2 text-right font-semibold">Last seen</th>
            <th className="px-3 py-2 text-left font-semibold">Status</th>
          </tr>
        </thead>
        <tbody>
          {issues.map((issue) => (
            <tr
              key={issue.fingerprint}
              onClick={() => onSelect(issue.fingerprint)}
              className={
                "cursor-pointer border-t border-neutral transition-colors hover:bg-base-200 " +
                (selected === issue.fingerprint ? "bg-primary/5" : "")
              }
            >
              <td className="max-w-md px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{issue.type}</span>
                  <Badge tone="neutral">{issue.source}</Badge>
                </div>
                <div className="truncate text-xs text-base-content/50">{issue.message}</div>
              </td>
              <td className="px-3 py-2 text-base-content/70">{issue.service}</td>
              <td className="px-3 py-2 text-right tabular-nums">{issue.count.toLocaleString()}</td>
              <td className="px-3 py-2 text-right text-xs text-base-content/60">
                {formatAgo(issue.lastSeen)}
              </td>
              <td className="px-3 py-2">
                <IssueStatusBadge issue={issue} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
