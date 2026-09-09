"use client";

import { useMemo, type ReactNode } from "react";
import { FilterX, ScrollText, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { useURLState } from "@/hooks/use-url-state";
import { LogTable } from "@/components/logs/log-table";
import { SEVERITY_OPTIONS } from "@/components/logs/logs-screen";
import type { LogRecord, MeshLogSources } from "@/lib/api-types";

// The three sources, in the order they are toggled. The subject's own lines;
// the node proxy's access lines, which name the pods; the waypoint's, which
// name the Service.
export const SOURCES = ["app", "ztunnel", "waypoint"] as const;
export type Source = (typeof SOURCES)[number];

// What the tab shows when the URL says nothing: the subject's own lines. The
// proxies' lines are opt-in — they are about it, not by it, and they outnumber
// it. This is the UI's default, not the hub's (which reads an absent source=
// as all three), so the request always spells it out.
const DEFAULT_SOURCES: readonly Source[] = ["app"];
const isDefault = (set: Set<Source>) =>
  set.size === DEFAULT_SOURCES.length && DEFAULT_SOURCES.every((s) => set.has(s));

// The URL keys this toolbar writes. They differ per page because a page may
// already spend `q` on something else — the mesh page spends it on the proxy
// list — and two controls writing one key would fight.
export interface SourceURLKeys {
  q: string;
  severity: string;
  source: string;
}

// The slice of an infinite log query this toolbar needs. Taking the parts
// rather than the query object keeps it independent of which hook fed it.
export interface SourcedLogsQuery {
  pages?: LogRecord[][];
  sources?: MeshLogSources;
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
}

// Reads the source set out of the URL. Exported because the caller needs it to
// build the request, and the toolbar must not be the only thing that knows how
// the parameter is spelled.
export function useSourceSet(key: string): { enabled: Set<Source>; param?: string } {
  const { get } = useURLState();
  const param = get(key);
  const enabled = useMemo<Set<Source>>(() => {
    const asked = (param ?? "")
      .split(",")
      .filter((s): s is Source => SOURCES.includes(s as Source));
    return new Set(asked.length ? asked : DEFAULT_SOURCES);
  }, [param]);
  return { enabled, param };
}

export const sourceParam = (enabled: Set<Source>) =>
  SOURCES.filter((s) => enabled.has(s)).join(",");

// One subject's logs from every source that has something to say about it, as
// one stream. Shared by the mesh workload page and the service page: the two
// answer the same question about the same lines, and a second toolbar would
// eventually disagree with this one about what a source means.
export function SourcedLogs({
  query,
  keys,
  appLabel,
  downloadName,
  emptyText,
  links,
  searchLabel = "Search logs",
  sourcesTestId = "sourced-log-sources",
}: {
  query: SourcedLogsQuery;
  keys: SourceURLKeys;
  // What to call the subject's own lines — its workload or service name.
  appLabel: string;
  downloadName: string;
  emptyText: ReactNode;
  // Where else these lines can be read, rendered into the sources line.
  links?: (sources: MeshLogSources) => ReactNode;
  // The search box's accessible name. It names its subject rather than the
  // control, so a screen reader on a page with more than one search says which
  // one this is.
  searchLabel?: string;
  // Names the sources line for the tests that already watch it by name.
  sourcesTestId?: string;
}) {
  const { get, setMany } = useURLState();
  const q = get(keys.q);
  const severity = get(keys.severity);
  const { enabled, param: srcParam } = useSourceSet(keys.source);

  const { pages, sources } = query;
  const empty = !query.isLoading && !pages?.some((p) => p.length > 0);
  const hasFilters = Boolean(q || severity || srcParam);
  // Without a workload behind the name there is nothing to narrow a proxy's
  // lines to, so the checkboxes are not offered — the reason is, instead.
  const proxiesOff = Boolean(sources?.proxiesUnavailable);
  const shown = proxiesOff ? (["app"] as const) : SOURCES;

  const toggle = (s: Source) => {
    const next = new Set(enabled);
    if (next.has(s)) next.delete(s);
    else next.add(s);
    // The default stays out of the URL.
    setMany({ [keys.source]: isDefault(next) ? undefined : sourceParam(next) });
  };

  return (
    <div className="flex flex-col gap-3" data-testid="sourced-logs">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
          <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
          <input
            type="search"
            defaultValue={q ?? ""}
            placeholder="Search message…"
            aria-label={searchLabel}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setMany({ [keys.q]: (e.target as HTMLInputElement).value || undefined });
              }
            }}
            className="h-9 w-64 bg-transparent text-sm outline-none placeholder:text-base-content/40"
          />
        </div>
        <Select
          ariaLabel="Minimum severity"
          className="w-40"
          value={severity ?? ""}
          onChange={(v) => setMany({ [keys.severity]: v || undefined })}
          options={SEVERITY_OPTIONS}
        />
        {shown.length > 1 && (
          <div className="flex items-center gap-3" role="group" aria-label="Log sources">
            {shown.map((s) => {
              const unavailable =
                s === "waypoint" && sources !== undefined && sources.waypoint.length === 0;
              // One source always stays on: an empty list would read as the
              // default and the box would snap back, so the last one is held.
              const last = enabled.size === 1 && enabled.has(s);
              const title = unavailable
                ? "No waypoint is bound to this workload"
                : last
                  ? "At least one source stays on"
                  : undefined;
              return (
                <label
                  key={s}
                  className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70"
                  title={title}
                >
                  <input
                    type="checkbox"
                    checked={enabled.has(s)}
                    disabled={unavailable || last}
                    onChange={() => toggle(s)}
                    className="accent-primary"
                  />
                  {s === "app" ? <span className="font-mono">{appLabel}</span> : s}
                </label>
              );
            })}
          </div>
        )}
        {hasFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              setMany({ [keys.q]: undefined, [keys.severity]: undefined, [keys.source]: undefined })
            }
          >
            <FilterX className="h-3.5 w-3.5" /> Clear
          </Button>
        )}
      </div>

      {sources && <SourcesLine sources={sources} links={links} testId={sourcesTestId} />}

      {empty ? (
        <EmptyState icon={ScrollText} title="No logs in this window">
          {emptyText}
        </EmptyState>
      ) : (
        <LogTable
          pages={pages}
          isLoading={query.isLoading}
          hasNextPage={query.hasNextPage}
          isFetchingNextPage={query.isFetchingNextPage}
          fetchNextPage={query.fetchNextPage}
          downloadName={downloadName}
          autoLoad
        />
      )}
    </div>
  );
}

// What was actually asked of the store, so an empty column is never silent —
// and, when the pods could not be known, why the proxies' lines were matched
// by name instead.
function SourcesLine({
  sources,
  links,
  testId,
}: {
  sources: MeshLogSources;
  links?: (sources: MeshLogSources) => ReactNode;
  testId: string;
}) {
  const pods = sources.precise ? sources.needles.filter((n) => !n.endsWith(".svc")).length : 0;
  const proxiesOff = Boolean(sources.proxiesUnavailable);
  return (
    <div
      className="flex flex-col gap-1 text-xs text-base-content/55"
      data-testid={testId}
    >
      <p className="flex flex-wrap gap-x-3">
        <span>
          app: <span className="font-mono">{sources.app.join(", ")}</span>
        </span>
        {!proxiesOff && (
          <>
            <span>
              ztunnel: {sources.precise ? `${pods} pod${pods === 1 ? "" : "s"} matched` : "matched by name"}
            </span>
            <span>
              waypoint:{" "}
              {sources.waypoint.length ? (
                <span className="font-mono">{sources.waypoint[0]}</span>
              ) : (
                "none bound"
              )}
            </span>
          </>
        )}
        {links?.(sources)}
      </p>
      {sources.fallback && <p className="text-warning">{sources.fallback}</p>}
      {sources.proxiesUnavailable && <p>{sources.proxiesUnavailable}</p>}
    </div>
  );
}
