"use client";

import { useMemo } from "react";
import { Bug, FilterX, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { Tabs } from "@/components/ui/tabs";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import {
  ISSUE_LIMIT,
  useErrorIssues,
  useErrorStats,
  type IssueFilters,
} from "@/hooks/use-errors-data";
import { ErrorStatsBand } from "./error-stats-band";
import { IssueList } from "./issue-list";
import { IssueDetailPanel } from "./issue-detail-panel";

const STATUS_TABS = [
  { value: "unresolved", label: "Unresolved" },
  { value: "resolved", label: "Resolved" },
  { value: "ignored", label: "Ignored" },
  { value: "all", label: "All" },
] as const;

const SORT_OPTIONS = [
  { value: "lastSeen", label: "Last seen" },
  { value: "count", label: "Events" },
  { value: "firstSeen", label: "First seen" },
] as const;

// Error issues: grouped, triageable exceptions. Status tab, service filter and
// search all live in the URL (shareable); the detail opens via ?issue=<hex>,
// which keeps the page a static export (no dynamic route segment).
export function ErrorsScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();

  const status = get("status") || "unresolved";
  const sort = get("sort") || "lastSeen";
  const selected = get("issue") ?? null;

  const filters: IssueFilters = useMemo(
    () => ({ status, service: get("service"), q: get("q"), sort }),
    [status, sort, get],
  );
  const hasFilters = Boolean(filters.service || filters.q);

  const issuesQuery = useErrorIssues(time, filters);
  const issues = issuesQuery.data?.issues ?? [];

  // Sort cannot change an aggregate, so the band's key stays stable across
  // re-sorts of the same set — no refetch when you reorder the table.
  const statsQuery = useErrorStats(time, {
    status: filters.status,
    service: filters.service,
    q: filters.q,
  });
  const stats = statsQuery.data;
  // Only claim a total when the page is full AND the server counted more.
  const truncated = issues.length >= ISSUE_LIMIT && (stats?.issues ?? 0) > issues.length;

  return (
    <div className="flex h-full min-h-0 gap-4">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-4">
        <Tabs
          items={STATUS_TABS.map((t) => ({ value: t.value, label: t.label }))}
          value={status}
          onChange={(v) => setMany({ status: v, issue: undefined })}
        />

        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
            <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
            <input
              type="search"
              defaultValue={filters.q ?? ""}
              placeholder="Search type or message…"
              aria-label="Search error type or message"
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  setMany({ q: (e.target as HTMLInputElement).value || undefined, issue: undefined });
                }
              }}
              className="h-8 w-56 bg-transparent text-sm outline-none"
            />
          </div>
          <input
            type="search"
            defaultValue={filters.service ?? ""}
            placeholder="Service…"
            aria-label="Filter by service"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setMany({ service: (e.target as HTMLInputElement).value || undefined, issue: undefined });
              }
            }}
            className="h-8 w-40 rounded-lg border border-neutral bg-base-200 px-2 text-sm outline-none"
          />
          <Select
            ariaLabel="Sort issues"
            value={sort}
            options={SORT_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
            onChange={(v) => setMany({ sort: v })}
          />
          {hasFilters ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setMany({ service: undefined, q: undefined })}
            >
              <FilterX className="mr-1 h-3.5 w-3.5" /> Clear
            </Button>
          ) : null}
        </div>

        <ErrorStatsBand
          stats={stats}
          isLoading={statsQuery.isLoading}
          activeService={filters.service ?? undefined}
          onPickService={(service) =>
            setMany({
              // A second click on the active service clears the filter.
              service: service === filters.service ? undefined : service,
              issue: undefined,
            })
          }
        />

        {/* The app shell owns no scrollbar (h-screen, overflow-hidden), so the
            list scrolls here — tabs, filters and the band stay put, and the
            detail panel keeps its own independent scroll. */}
        <div className="min-h-0 flex-1 overflow-y-auto" data-testid="issues-scroll">
          {issuesQuery.isLoading ? (
            <CenteredSpinner />
          ) : issues.length === 0 ? (
            <EmptyState icon={Bug} title="No issues here">
              Nothing matches these filters. Errors appear automatically from your
              traces and logs — no instrumentation needed.
            </EmptyState>
          ) : (
            <>
              <IssueList
                issues={issues}
                selected={selected}
                onSelect={(fp) => setMany({ issue: fp })}
              />
              {truncated ? (
                <p
                  data-testid="issues-footer"
                  className="px-3 py-2 text-xs text-base-content/50"
                >
                  Showing {issues.length.toLocaleString()} of{" "}
                  {stats?.issues.toLocaleString()} issues — narrow the filters to
                  see the rest.
                </p>
              ) : null}
            </>
          )}
        </div>
      </div>

      {selected ? (
        <IssueDetailPanel
          fingerprint={selected}
          time={time}
          onClose={() => setMany({ issue: undefined })}
        />
      ) : null}
    </div>
  );
}
