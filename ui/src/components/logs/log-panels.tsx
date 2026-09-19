"use client";

import { useState } from "react";
import { ChevronDown, ChevronRight, Maximize2, Minimize2, Pause, Radio } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useLogSearch, type LogFilters } from "@/hooks/use-logs-data";
import { cn } from "@/lib/cn";
import { formatTime } from "@/lib/format";
import { type TimeParams } from "@/lib/query-keys";
import { LogTable } from "./log-table";
import { ResolutionNotices } from "./resolution-notices";

export interface PanelSubject {
  kind: "service" | "workload";
  value: string;
  label: string;
}

// How one panel is laid out. Component state, not URL state: whether a panel
// is folded or followed is a gesture on this screen, not part of what a
// pasted link should say.
interface PanelLayout {
  collapsed?: boolean;
  wide?: boolean;
  follow?: boolean;
}

const subjectKey = (subject: PanelSubject) => `${subject.kind}:${subject.value}`;

// One panel per chosen service or workload, each with its own query, paging,
// scroll box and controls. liveWindowMs is the length of the window a followed
// panel tails; null when the global range is absolute, since a window that
// ends in the past has nothing to follow.
export function ServicePanels({ time, filters, services, workloads, liveWindowMs }: {
  time: TimeParams;
  filters: LogFilters;
  services: string[];
  workloads: string[];
  liveWindowMs: number | null;
}) {
  const [layouts, setLayouts] = useState<Record<string, PanelLayout>>({});
  const subjects: PanelSubject[] = [
    ...services.map((value) => ({ kind: "service" as const, value, label: value })),
    ...workloads.map((value) => ({ kind: "workload" as const, value, label: value })),
  ];
  if (subjects.length === 0) return <Card className="p-8 text-center text-sm text-base-content/60">Choose one or more services to open panels</Card>;

  const update = (key: string, patch: PanelLayout) =>
    setLayouts((current) => ({ ...current, [key]: { ...current[key], ...patch } }));
  const setAll = (collapsed: boolean) =>
    setLayouts((current) => Object.fromEntries(subjects.map((subject) => {
      const key = subjectKey(subject);
      return [key, { ...current[key], collapsed }];
    })));
  const allCollapsed = subjects.every((subject) => layouts[subjectKey(subject)]?.collapsed);
  const noneCollapsed = subjects.every((subject) => !layouts[subjectKey(subject)]?.collapsed);

  return (
    <div className="flex flex-col gap-3" data-testid="log-panels">
      <div className="flex flex-wrap items-center gap-2 text-xs text-base-content/60">
        <span>{subjects.length} {subjects.length === 1 ? "panel" : "panels"}</span>
        <Button variant="ghost" size="sm" onClick={() => setAll(true)} disabled={allCollapsed}>Collapse all</Button>
        <Button variant="ghost" size="sm" onClick={() => setAll(false)} disabled={noneCollapsed}>Expand all</Button>
      </div>
      <div className="grid gap-4 xl:grid-cols-2">
        {subjects.map((subject) => {
          const key = subjectKey(subject);
          return (
            <LogPanel
              key={key}
              subject={subject}
              time={time}
              filters={filters}
              layout={layouts[key] ?? {}}
              liveWindowMs={liveWindowMs}
              onLayout={(patch) => update(key, patch)}
            />
          );
        })}
      </div>
    </div>
  );
}

function LogPanel({ subject, time, filters, layout, liveWindowMs, onLayout }: {
  subject: PanelSubject;
  time: TimeParams;
  filters: LogFilters;
  layout: PanelLayout;
  liveWindowMs: number | null;
  onLayout: (patch: PanelLayout) => void;
}) {
  const collapsed = Boolean(layout.collapsed);
  const following = Boolean(layout.follow) && liveWindowMs !== null;
  const search = useLogSearch(time, {
    ...filters,
    services: subject.kind === "service" ? [subject.value] : [],
    workloads: subject.kind === "workload" ? [subject.value] : [],
  }, !collapsed, true, following ? liveWindowMs : undefined);
  const loaded = search.data?.pages.reduce((sum, page) => sum + page.logs.length, 0) ?? 0;
  const bodyId = `log-panel-${subjectKey(subject).replace(/[^a-z0-9]+/gi, "-")}`;

  return (
    <section
      aria-label={`Logs for ${subject.label}`}
      className={cn("min-w-0 rounded-xl border border-neutral bg-base-200 p-3", layout.wide && "xl:col-span-2")}
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => onLayout({ collapsed: !collapsed })}
          aria-expanded={!collapsed}
          aria-controls={bodyId}
          aria-label={collapsed ? `Expand ${subject.label}` : `Collapse ${subject.label}`}
          className="inline-flex min-w-0 items-center gap-1 text-base-content/70 hover:text-base-content"
        >
          {collapsed ? <ChevronRight className="h-4 w-4 shrink-0" aria-hidden /> : <ChevronDown className="h-4 w-4 shrink-0" aria-hidden />}
          <span className="truncate font-mono text-sm font-semibold text-base-content">{subject.label}</span>
        </button>
        {search.data && (
          <span className="text-xs text-base-content/55">
            {loaded} {loaded === 1 ? "line" : "lines"}{search.hasNextPage ? "+" : ""}
          </span>
        )}
        {following && (
          <span className="inline-flex items-center gap-1 text-xs text-success" role="status">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-success" aria-hidden />
            Live{search.dataUpdatedAt ? ` · ${formatTime(new Date(search.dataUpdatedAt).toISOString())}` : ""}
          </span>
        )}
        <span className="ml-auto inline-flex items-center gap-1">
          <button
            type="button"
            onClick={() => onLayout({ follow: !layout.follow })}
            aria-pressed={following}
            disabled={liveWindowMs === null}
            title={liveWindowMs === null ? "Following needs a relative time range" : following ? "Stop following" : "Follow the newest lines"}
            className={cn(
              "inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs disabled:opacity-40",
              following ? "bg-primary text-primary-content" : "text-base-content/60 hover:bg-base-300 hover:text-base-content",
            )}
          >
            {following ? <Pause className="h-3.5 w-3.5" aria-hidden /> : <Radio className="h-3.5 w-3.5" aria-hidden />}
            Follow
          </button>
          <button
            type="button"
            onClick={() => onLayout({ wide: !layout.wide })}
            aria-pressed={Boolean(layout.wide)}
            aria-label={layout.wide ? `Narrow ${subject.label}` : `Widen ${subject.label}`}
            title={layout.wide ? "Back to half width" : "Span the full width"}
            className="hidden h-7 w-7 items-center justify-center rounded-md text-base-content/60 hover:bg-base-300 hover:text-base-content xl:inline-flex"
          >
            {layout.wide ? <Minimize2 className="h-3.5 w-3.5" aria-hidden /> : <Maximize2 className="h-3.5 w-3.5" aria-hidden />}
          </button>
        </span>
      </div>
      <div id={bodyId} hidden={collapsed}>
        <ResolutionNotices resolutions={search.data?.pages[0]?.resolutions} sources={filters.source} />
        {search.isError ? <p className="p-4 text-sm text-error">Unable to load this panel.</p> : (
          <LogTable
            pages={search.data?.pages.map((page) => page.logs)}
            isLoading={search.isLoading}
            hasNextPage={Boolean(search.hasNextPage)}
            isFetchingNextPage={search.isFetchingNextPage}
            fetchNextPage={() => search.fetchNextPage()}
            autoLoad
            bounded
            heightClass={layout.wide ? "max-h-[80vh]" : "max-h-[65vh]"}
            pinTop={following}
            downloadName={subject.label.replace("/", "-")}
          />
        )}
      </div>
    </section>
  );
}
