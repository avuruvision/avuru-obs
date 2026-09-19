"use client";

import { useMemo } from "react";
import { Columns2, FilterX, Rows3, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { MultiCombobox, type MultiComboboxOption } from "@/components/ui/multi-combobox";
import { Select } from "@/components/ui/select";
import { TagChips } from "@/components/filters/tag-chips";
import { useLogSearch, useLogServices, type LogFilters } from "@/hooks/use-logs-data";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { cn } from "@/lib/cn";
import { type TimeParams } from "@/lib/query-keys";
import { LogTable } from "./log-table";
import { ServicePanels } from "./log-panels";
import { ResolutionNotices } from "./resolution-notices";

export const SEVERITY_OPTIONS = [
  { value: "", label: "All severities" },
  { value: "INFO", label: "INFO+" },
  { value: "WARN", label: "WARN+" },
  { value: "ERROR", label: "ERROR+" },
];

const SOURCE_OPTIONS = [
  { value: "app", label: "Application" },
  { value: "ztunnel", label: "ztunnel" },
  { value: "waypoint", label: "waypoint" },
  { value: "other", label: "Other" },
] as const;
const ALL_SOURCES = SOURCE_OPTIONS.map((source) => source.value);
// The URL keys for the text filters. The mesh page already spends `q` on its
// proxy list, so there the explorer's own filters are prefixed — two controls
// writing one key would fight (the convention sourced-logs.tsx follows).
const URL_KEYS = {
  signals: { q: "q", severity: "severity", tags: "tags" },
  mesh: { q: "lq", severity: "lsev", tags: "ltags" },
} as const;
const csv = (value?: string) => [...new Set((value ?? "").split(",").map((part) => part.trim()).filter(Boolean))];

export function LogsScreen({ context = "signals" }: { context?: "signals" | "mesh" }) {
  const { time, custom, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();
  const keys = URL_KEYS[context];
  const suggestions = useLogServices(time);
  const services = csv(get("services") ?? get("service"));
  const workloads = csv(get("workloads"));
  const explicitSources = csv(get("sources"));
  const enabledSources = explicitSources.length ? explicitSources : ALL_SOURCES;
  const display = get("display") === "panels" ? "panels" : "merged";
  const targets = [...services.map((service) => `service:${service}`), ...workloads.map((workload) => `workload:${workload}`)];
  const options = useMemo<MultiComboboxOption[]>(() => [
    ...(suggestions.data?.services ?? []).map((service) => ({ value: `service:${service}`, label: service })),
    ...(suggestions.data?.workloads ?? []).map((workload) => {
      const slash = workload.indexOf("/");
      return {
        value: `workload:${workload}`,
        label: slash < 0 ? workload : workload.slice(slash + 1),
        description: slash < 0 ? "workload" : `${workload.slice(0, slash)} · workload`,
      };
    }),
  ], [suggestions.data]);
  const filters: LogFilters = {
    services,
    workloads,
    source: enabledSources.join(","),
    severity: get(keys.severity),
    q: get(keys.q),
    tags: get(keys.tags),
  };
  const hasFilters = targets.length > 0 || Boolean(filters.severity || filters.q || filters.tags || explicitSources.length);
  const setTargets = (values: string[]) => setMany({
    services: values.filter((value) => value.startsWith("service:")).map((value) => value.slice(8)).join(",") || undefined,
    workloads: values.filter((value) => value.startsWith("workload:")).map((value) => value.slice(9)).join(",") || undefined,
    service: undefined,
  });
  const toggleSource = (source: string) => {
    const next = enabledSources.includes(source)
      ? enabledSources.filter((item) => item !== source)
      : ALL_SOURCES.filter((item) => item === source || enabledSources.includes(item));
    if (next.length > 0) setMany({ sources: next.length === ALL_SOURCES.length ? undefined : next.join(",") });
  };

  return (
    <div className="flex flex-col gap-4" data-testid={`${context}-log-explorer`}>
      <div className="flex flex-wrap items-start gap-2">
        <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
          <Search className="h-3.5 w-3.5 text-muted" aria-hidden />
          <input
            key={filters.q ?? ""}
            type="search"
            defaultValue={filters.q ?? ""}
            placeholder="Search message…"
            aria-label="Search log message"
            onKeyDown={(event) => {
              if (event.key === "Enter") setMany({ [keys.q]: event.currentTarget.value || undefined });
            }}
            className="h-9 w-64 bg-transparent text-sm outline-none placeholder:text-base-content/40"
          />
        </div>
        <MultiCombobox
          selected={targets}
          options={options}
          onChange={setTargets}
          ariaLabel="Select log services"
          placeholder="Services…"
          allowCustom={(text) => ({ value: `service:${text}`, label: text })}
          className="w-80"
        />
        <Select ariaLabel="Minimum severity" className="w-44" value={filters.severity ?? ""} onChange={(value) => setMany({ [keys.severity]: value || undefined })} options={SEVERITY_OPTIONS} />
        <div className="inline-flex h-9 rounded-lg border border-neutral bg-base-200 p-1" aria-label="Log display">
          <DisplayButton active={display === "merged"} label="Merged stream" onClick={() => setMany({ display: undefined })}><Rows3 className="h-3.5 w-3.5" aria-hidden /></DisplayButton>
          <DisplayButton active={display === "panels"} label="Service panels" onClick={() => setMany({ display: "panels" })}><Columns2 className="h-3.5 w-3.5" aria-hidden /></DisplayButton>
        </div>
        {hasFilters && (
          <Button variant="ghost" size="sm" onClick={() => setMany({ services: undefined, workloads: undefined, service: undefined, sources: undefined, [keys.severity]: undefined, [keys.q]: undefined, [keys.tags]: undefined })}>
            <FilterX className="h-3.5 w-3.5" /> Clear
          </Button>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="text-xs font-medium text-base-content/60">Sources</span>
        {SOURCE_OPTIONS.map((source) => {
          const checked = enabledSources.includes(source.value);
          return (
            <label key={source.value} className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/75">
              <input type="checkbox" checked={checked} disabled={checked && enabledSources.length === 1} onChange={() => toggleSource(source.value)} className="accent-primary" />
              {source.label}
            </label>
          );
        })}
        <TagChips value={filters.tags} onChange={(next) => setMany({ [keys.tags]: next })} />
      </div>

      {display === "merged" ? <MergedLogs time={time} filters={filters} /> : <ServicePanels time={time} filters={filters} services={services} workloads={workloads} liveWindowMs={custom ? null : windowMs} />}
    </div>
  );
}

function DisplayButton({ active, label, onClick, children }: { active: boolean; label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" aria-label={label} aria-pressed={active} onClick={onClick} className={cn("inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs", active ? "bg-primary text-primary-content" : "text-base-content/60 hover:text-base-content")}>
      {children}{label}
    </button>
  );
}

function MergedLogs({ time, filters }: { time: TimeParams; filters: LogFilters }) {
  const search = useLogSearch(time, filters);
  return (
    <div className="flex flex-col gap-3">
      <ResolutionNotices resolutions={search.data?.pages[0]?.resolutions} sources={filters.source} />
      <LogTable pages={search.data?.pages.map((page) => page.logs)} isLoading={search.isLoading} hasNextPage={Boolean(search.hasNextPage)} isFetchingNextPage={search.isFetchingNextPage} fetchNextPage={() => search.fetchNextPage()} autoLoad />
    </div>
  );
}
