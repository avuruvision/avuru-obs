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
import { useErrorIssues, type IssueFilters } from "@/hooks/use-errors-data";
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

  return (
    <div className="flex h-full gap-4">
      <div className="flex min-w-0 flex-1 flex-col gap-4">
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

        {issuesQuery.isLoading ? (
          <CenteredSpinner />
        ) : issues.length === 0 ? (
          <EmptyState icon={Bug} title="No issues here">
            Nothing matches these filters. Errors appear automatically from your
            traces and logs — no instrumentation needed.
          </EmptyState>
        ) : (
          <IssueList
            issues={issues}
            selected={selected}
            onSelect={(fp) => setMany({ issue: fp })}
          />
        )}
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
