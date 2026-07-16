import { X } from "lucide-react";
import Link from "next/link";
import { formatAgo } from "@/lib/format";
import type { ErrorIssueStatus } from "@/lib/api-types";
import type { TimeParams } from "@/lib/query-keys";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import {
  useErrorIssue,
  useErrorIssueEvents,
  useErrorIssueHistogram,
  useSetIssueStatus,
} from "@/hooks/use-errors-data";
import { IssueStatusBadge } from "./issue-status-badge";
import { OccurrenceHistogram } from "./occurrence-histogram";
import { Stacktrace } from "./stacktrace";

// Slide-over detail for one issue. Driven by ?issue=<fingerprint> so the view
// is shareable and the page stays a static export (no dynamic route segment).
export function IssueDetailPanel({
  fingerprint,
  time,
  onClose,
}: {
  fingerprint: string;
  time: TimeParams;
  onClose: () => void;
}) {
  const issueQuery = useErrorIssue(fingerprint);
  const histogram = useErrorIssueHistogram(fingerprint, time);
  const events = useErrorIssueEvents(fingerprint);
  const setStatus = useSetIssueStatus();

  const issue = issueQuery.data;
  const latest = events.data?.pages[0]?.events[0];

  const triage = (status: ErrorIssueStatus) =>
    setStatus.mutate({ fingerprint, status });

  return (
    <aside className="flex w-full max-w-xl flex-col overflow-y-auto border-l border-neutral bg-base-100">
      <header className="flex items-start justify-between gap-3 border-b border-neutral p-4">
        <div className="min-w-0">
          {issue ? (
            <>
              <div className="flex items-center gap-2">
                <h2 className="truncate font-semibold">{issue.type}</h2>
                <IssueStatusBadge issue={issue} />
              </div>
              <p className="truncate text-sm text-base-content/60">{issue.message}</p>
            </>
          ) : (
            <h2 className="font-semibold">Issue</h2>
          )}
        </div>
        <button
          onClick={onClose}
          aria-label="Close issue detail"
          className="rounded-lg p-1 text-base-content/50 hover:bg-base-300 hover:text-base-content"
        >
          <X className="h-4 w-4" />
        </button>
      </header>

      {issueQuery.isLoading ? (
        <div className="p-8">
          <CenteredSpinner />
        </div>
      ) : !issue ? (
        <p className="p-4 text-sm text-error">Couldn’t load this issue.</p>
      ) : (
        <div className="flex flex-col gap-5 p-4">
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant={issue.status === "resolved" && !issue.regressed ? "primary" : "secondary"}
              disabled={setStatus.isPending}
              onClick={() => triage("resolved")}
            >
              Resolve
            </Button>
            <Button
              size="sm"
              variant={issue.status === "ignored" ? "primary" : "secondary"}
              disabled={setStatus.isPending}
              onClick={() => triage("ignored")}
            >
              Ignore
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={setStatus.isPending}
              onClick={() => triage("unresolved")}
            >
              Reopen
            </Button>
          </div>

          <dl className="grid grid-cols-3 gap-3 text-sm">
            <Stat label="Service" value={issue.service} />
            <Stat label="Events" value={issue.count.toLocaleString()} />
            <Stat label="Source" value={issue.source} />
            <Stat label="First seen" value={formatAgo(issue.firstSeen)} />
            <Stat label="Last seen" value={formatAgo(issue.lastSeen)} />
            {latest?.environment ? <Stat label="Environment" value={latest.environment} /> : null}
          </dl>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-base-content/50">
              Occurrences
            </h3>
            {histogram.data ? <OccurrenceHistogram points={histogram.data.points} /> : null}
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-base-content/50">
              Latest stack trace
            </h3>
            <Stacktrace trace={latest?.stacktrace ?? ""} />
            {latest?.traceId ? (
              <Link
                href={`/traces?trace=${latest.traceId}&tab=traces`}
                className="mt-2 inline-block text-xs font-medium text-primary hover:underline"
              >
                View originating trace →
              </Link>
            ) : null}
          </section>
        </div>
      )}
    </aside>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-base-content/50">{label}</dt>
      <dd className="truncate font-medium">{value}</dd>
    </div>
  );
}
