"use client";

import { useMemo } from "react";
import { Columns2, FilterX, Rows3, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { MultiCombobox, type MultiComboboxOption } from "@/components/ui/multi-combobox";
import { Select } from "@/components/ui/select";
import { TagChips } from "@/components/filters/tag-chips";
import { useLogSearch, useLogServices, type LogFilters } from "@/hooks/use-logs-data";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { cn } from "@/lib/cn";
import type { LogResolution } from "@/lib/api-types";
import { type TimeParams } from "@/lib/query-keys";
import { LogTable } from "./log-table";

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
const csv = (value?: string) => [...new Set((value ?? "").split(",").map((part) => part.trim()).filter(Boolean))];

export function LogsScreen({ context = "signals" }: { context?: "signals" | "mesh" }) {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
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
    severity: get("severity"),
    q: get("q"),
    tags: get("tags"),
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
              if (event.key === "Enter") setMany({ q: event.currentTarget.value || undefined });
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
        <Select ariaLabel="Minimum severity" className="w-44" value={filters.severity ?? ""} onChange={(value) => setMany({ severity: value || undefined })} options={SEVERITY_OPTIONS} />
        <div className="inline-flex h-9 rounded-lg border border-neutral bg-base-200 p-1" aria-label="Log display">
          <DisplayButton active={display === "merged"} label="Merged stream" onClick={() => setMany({ display: undefined })}><Rows3 className="h-3.5 w-3.5" aria-hidden /></DisplayButton>
          <DisplayButton active={display === "panels"} label="Service panels" onClick={() => setMany({ display: "panels" })}><Columns2 className="h-3.5 w-3.5" aria-hidden /></DisplayButton>
        </div>
        {hasFilters && (
          <Button variant="ghost" size="sm" onClick={() => setMany({ services: undefined, workloads: undefined, service: undefined, sources: undefined, severity: undefined, q: undefined, tags: undefined })}>
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
        <TagChips value={filters.tags} onChange={(next) => setMany({ tags: next })} />
      </div>

      {display === "merged" ? <MergedLogs time={time} filters={filters} /> : <ServicePanels time={time} filters={filters} services={services} workloads={workloads} />}
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

function ServicePanels({ time, filters, services, workloads }: { time: TimeParams; filters: LogFilters; services: string[]; workloads: string[] }) {
  const subjects = [
    ...services.map((value) => ({ kind: "service" as const, value, label: value })),
    ...workloads.map((value) => ({ kind: "workload" as const, value, label: value })),
  ];
  if (subjects.length === 0) return <Card className="p-8 text-center text-sm text-base-content/60">Choose one or more services to open panels</Card>;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      {subjects.map((subject) => <LogPanel key={`${subject.kind}:${subject.value}`} subject={subject} time={time} filters={filters} />)}
    </div>
  );
}

function LogPanel({ subject, time, filters }: { subject: { kind: "service" | "workload"; value: string; label: string }; time: TimeParams; filters: LogFilters }) {
  const search = useLogSearch(time, {
    ...filters,
    services: subject.kind === "service" ? [subject.value] : [],
    workloads: subject.kind === "workload" ? [subject.value] : [],
  }, true, true);
  return (
    <section aria-label={`Logs for ${subject.label}`} className="min-w-0 rounded-xl border border-neutral bg-base-200 p-3">
      <h2 className="mb-3 truncate font-mono text-sm font-semibold">{subject.label}</h2>
      <ResolutionNotices resolutions={search.data?.pages[0]?.resolutions} sources={filters.source} />
      {search.isError ? <p className="p-4 text-sm text-error">Unable to load this panel.</p> : (
        <LogTable pages={search.data?.pages.map((page) => page.logs)} isLoading={search.isLoading} hasNextPage={Boolean(search.hasNextPage)} isFetchingNextPage={search.isFetchingNextPage} fetchNextPage={() => search.fetchNextPage()} autoLoad downloadName={subject.label.replace("/", "-")} />
      )}
    </section>
  );
}

function ResolutionNotices({ resolutions, sources }: { resolutions?: LogResolution[]; sources?: string }) {
  if (!sources?.split(",").some((source) => source === "ztunnel" || source === "waypoint")) return null;
  const notices = [...new Set((resolutions ?? []).flatMap((resolution) => {
    const subject = resolution.service || [resolution.namespace, resolution.workload].filter(Boolean).join("/");
    return [resolution.proxiesUnavailable, resolution.proxiesFallback]
      .filter(Boolean)
      .map((message) => `${subject}: ${message}`);
  }))];
  if (notices.length === 0) return null;
  return (
    <div role="status" className="mb-3 rounded-lg border border-warning/50 bg-warning/10 px-3 py-2 text-xs text-base-content/75">
      {notices.map((notice) => <p key={notice}>{notice}</p>)}
    </div>
  );
}
