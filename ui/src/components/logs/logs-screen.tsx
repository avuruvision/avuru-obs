"use client";

import { useMemo } from "react";
import { FilterX, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useLogSearch, type LogFilters } from "@/hooks/use-logs-data";
import { LogTable } from "./log-table";

const SEVERITY_OPTIONS = [
  { value: "", label: "All severities" },
  { value: "INFO", label: "INFO+" },
  { value: "WARN", label: "WARN+" },
  { value: "ERROR", label: "ERROR+" },
];

// Full-text log search with severity/service filters, all held in the URL so a
// filtered view is shareable. A row's trace id links back to its trace
// (log→trace correlation).
export function LogsScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();

  const filters: LogFilters = useMemo(
    () => ({ service: get("service"), severity: get("severity"), q: get("q") }),
    [get],
  );
  const hasFilters = Boolean(filters.service || filters.severity || filters.q);

  const search = useLogSearch(time, filters);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
          <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
          <input
            type="search"
            defaultValue={filters.q ?? ""}
            placeholder="Search message…"
            aria-label="Search log message"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setMany({ q: (e.target as HTMLInputElement).value || undefined });
              }
            }}
            className="h-9 w-64 bg-transparent text-sm outline-none placeholder:text-base-content/40"
          />
        </div>
        <input
          type="text"
          defaultValue={filters.service ?? ""}
          placeholder="service"
          aria-label="Filter by service"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              setMany({ service: (e.target as HTMLInputElement).value || undefined });
            }
          }}
          className="h-9 w-40 rounded-lg border border-neutral bg-base-200 px-3 text-sm outline-none placeholder:text-base-content/40"
        />
        <Select
          ariaLabel="Minimum severity"
          className="w-44"
          value={filters.severity ?? ""}
          onChange={(v) => setMany({ severity: v || undefined })}
          options={SEVERITY_OPTIONS}
        />
        {hasFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setMany({ service: undefined, severity: undefined, q: undefined })}
          >
            <FilterX className="h-3.5 w-3.5" /> Clear
          </Button>
        )}
      </div>

      <LogTable
        pages={search.data?.pages.map((p) => p.logs)}
        isLoading={search.isLoading}
        hasNextPage={Boolean(search.hasNextPage)}
        isFetchingNextPage={search.isFetchingNextPage}
        fetchNextPage={() => search.fetchNextPage()}
      />
    </div>
  );
}
