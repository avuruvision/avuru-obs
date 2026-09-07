"use client";

import Link from "next/link";
import { useMemo } from "react";
import { FilterX, ScrollText, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshWorkloadLogs } from "@/hooks/use-mesh-data";
import { LogTable } from "@/components/logs/log-table";
import { SEVERITY_OPTIONS } from "@/components/logs/logs-screen";
import type { MeshLogSources } from "@/lib/api-types";

// The three sources, in the order they are toggled. "app" is the workload's
// own lines; the other two are what the proxies wrote about it.
const SOURCES = ["app", "ztunnel", "waypoint"] as const;
type Source = (typeof SOURCES)[number];

// What the tab shows when the URL says nothing: the workload's own lines. The
// proxies' lines are opt-in — they are about the workload, not by it. This is
// the UI's default, not the hub's (which reads an absent source= as all
// three), so the request always spells it out.
const DEFAULT_SOURCES: readonly Source[] = ["app"];
const isDefault = (set: Set<Source>) =>
  set.size === DEFAULT_SOURCES.length && DEFAULT_SOURCES.every((s) => set.has(s));

// A workload's logs from all three sources, one stream. The toolbar is this
// tab's own rather than the logs screen's: that one is welded to the URL keys
// of its page (q, service, severity), and this page already uses q for the
// proxy list — so the keys here are wlq, wlsev and wlsrc.
export function WorkloadLogs({
  namespace,
  name,
  waypoint,
}: {
  namespace: string;
  name: string;
  // "namespace/name" of the bound waypoint, handed to the hub for the case
  // where it cannot resolve the binding itself.
  waypoint?: string;
}) {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const q = get("wlq");
  const severity = get("wlsev");
  const srcParam = get("wlsrc");
  const enabled = useMemo<Set<Source>>(() => {
    const asked = (srcParam ?? "").split(",").filter((s): s is Source => SOURCES.includes(s as Source));
    return new Set(asked.length ? asked : DEFAULT_SOURCES);
  }, [srcParam]);

  const logs = useMeshWorkloadLogs(time, true, namespace, name, {
    q,
    severity,
    source: SOURCES.filter((s) => enabled.has(s)).join(","),
    waypoint,
  });
  const pages = logs.data?.pages.map((p) => p.logs);
  const sources = logs.data?.pages[0]?.sources;
  const empty = !logs.isLoading && !pages?.some((p) => p.length > 0);
  const hasFilters = Boolean(q || severity || srcParam);

  const toggle = (s: Source) => {
    const next = new Set(enabled);
    if (next.has(s)) next.delete(s);
    else next.add(s);
    // The default stays out of the URL.
    setMany({ wlsrc: isDefault(next) ? undefined : SOURCES.filter((x) => next.has(x)).join(",") });
  };

  return (
    <div className="flex flex-col gap-3" data-testid="mesh-workload-logs">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
          <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
          <input
            type="search"
            defaultValue={q ?? ""}
            placeholder="Search message…"
            aria-label="Search workload logs"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setMany({ wlq: (e.target as HTMLInputElement).value || undefined });
              }
            }}
            className="h-9 w-64 bg-transparent text-sm outline-none placeholder:text-base-content/40"
          />
        </div>
        <Select
          ariaLabel="Minimum severity"
          className="w-40"
          value={severity ?? ""}
          onChange={(v) => setMany({ wlsev: v || undefined })}
          options={SEVERITY_OPTIONS}
        />
        <div className="flex items-center gap-3" role="group" aria-label="Log sources">
          {SOURCES.map((s) => {
            const unavailable = s === "waypoint" && sources !== undefined && sources.waypoint.length === 0;
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
                {s === "app" ? <span className="font-mono">{name}</span> : s}
              </label>
            );
          })}
        </div>
        {hasFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setMany({ wlq: undefined, wlsev: undefined, wlsrc: undefined })}
          >
            <FilterX className="h-3.5 w-3.5" /> Clear
          </Button>
        )}
      </div>

      {sources && <SourcesLine sources={sources} name={name} />}

      {empty ? (
        <EmptyState icon={ScrollText} title="No logs in this window">
          Neither this workload nor the proxies carrying it logged anything here
          that matches. Widen the time range, or check that its logs reach the
          collector.
        </EmptyState>
      ) : (
        <LogTable
          pages={pages}
          isLoading={logs.isLoading}
          hasNextPage={Boolean(logs.hasNextPage)}
          isFetchingNextPage={logs.isFetchingNextPage}
          fetchNextPage={() => logs.fetchNextPage()}
          autoLoad
        />
      )}
    </div>
  );
}

// What was actually asked of the store, so an empty column is never silent —
// and, when the pods could not be known, why the proxies' lines were matched
// by name instead.
function SourcesLine({ sources, name }: { sources: MeshLogSources; name: string }) {
  const pods = sources.precise ? sources.needles.filter((n) => !n.endsWith(".svc")).length : 0;
  return (
    <div className="flex flex-col gap-1 text-xs text-base-content/55" data-testid="mesh-workload-log-sources">
      <p className="flex flex-wrap gap-x-3">
        <span>
          app: <span className="font-mono">{sources.app[0]}</span>
        </span>
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
        <Link
          href={`/logs?service=${encodeURIComponent(name)}`}
          className="text-primary hover:underline"
        >
          open the app lines in Logs
        </Link>
      </p>
      {sources.fallback && <p className="text-warning">{sources.fallback}</p>}
    </div>
  );
}
