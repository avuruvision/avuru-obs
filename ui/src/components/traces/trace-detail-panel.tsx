"use client";

import { GitCompare, Maximize2, Minimize2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { cn } from "@/lib/cn";
import { useTrace } from "@/hooks/use-traces-data";
import { useURLState } from "@/hooks/use-url-state";
import type { Span } from "@/lib/api-types";
import { Waterfall } from "./waterfall";
import { SpanDetail } from "./span-detail";
import { TraceSummaryBar } from "./trace-summary-bar";
import { SpansTable } from "./views/spans-table";
import { Flamegraph } from "./views/flamegraph";
import { TraceStats } from "./views/trace-stats";
import { TraceTree } from "./views/trace-tree";
import { TraceJson } from "./views/trace-json";
import { TraceDiff } from "./views/trace-diff";

// "Tree" replaced the aggregated graph but keeps the "graph" URL value so
// shared links stay valid.
const VIEWS = [
  { value: "timeline", label: "Timeline" },
  { value: "spans", label: "Spans" },
  { value: "flame", label: "Flamegraph" },
  { value: "stats", label: "Statistics" },
  { value: "graph", label: "Tree" },
  { value: "json", label: "JSON" },
] as const;

type View = (typeof VIEWS)[number]["value"];

// The right side of the split workspace: header (view switcher / compare /
// maximize / close), trace summary bar (stats + service legend), and a body
// that is either the active single-trace view + span drawer, or the
// comparison diff.
export function TraceDetailPanel({
  traceId,
  compareId,
  fullscreen,
}: {
  traceId: string;
  compareId?: string | null;
  fullscreen: boolean;
}) {
  const { get, setMany } = useURLState();
  const a = useTrace(traceId);
  const b = useTrace(compareId ?? null);

  const comparing = Boolean(compareId);
  const view = (VIEWS.find((v) => v.value === get("view"))?.value ?? "timeline") as View;
  const selectedSpanId = get("span") ?? null;

  const trace = a.data;
  const selectedSpan = trace?.spans.find((s) => s.spanId === selectedSpanId) ?? null;

  const close = () =>
    setMany({ trace: undefined, view: undefined, span: undefined, compare: undefined, full: undefined });
  const onSelectSpan = (span: Span) =>
    setMany({ span: span.spanId === selectedSpanId ? undefined : span.spanId });

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-neutral bg-base-200">
      <header className="flex flex-wrap items-center gap-2 border-b border-neutral px-3 py-2">
        <span className="truncate font-mono text-xs font-semibold">{traceId}</span>

        <div className="ml-auto flex items-center gap-1.5">
          {!comparing && (
            <div className="flex overflow-hidden rounded-lg border border-neutral">
              {VIEWS.map((v) => (
                <button
                  key={v.value}
                  onClick={() => setMany({ view: v.value === "timeline" ? undefined : v.value })}
                  className={cn(
                    "px-2 py-1 text-xs font-medium transition-colors",
                    v.value === view
                      ? "bg-primary text-primary-content"
                      : "text-base-content/60 hover:bg-base-300 hover:text-base-content",
                  )}
                >
                  {v.label}
                </button>
              ))}
            </div>
          )}
          {comparing && (
            <Button variant="ghost" size="sm" onClick={() => setMany({ compare: undefined })}>
              <GitCompare className="h-3.5 w-3.5" /> Exit compare
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            aria-label={fullscreen ? "Exit full screen" : "Full screen"}
            onClick={() => setMany({ full: fullscreen ? undefined : "1" })}
          >
            {fullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
          </Button>
          <Button variant="ghost" size="icon" aria-label="Close trace" onClick={close}>
            <X className="h-4 w-4" />
          </Button>
        </div>
      </header>

      {!comparing && trace && <TraceSummaryBar trace={trace} />}

      <div className="flex min-h-0 flex-1">
        <div className="min-h-0 flex-1 overflow-auto p-3">
          {comparing ? (
            a.isLoading || b.isLoading ? (
              <CenteredSpinner />
            ) : a.data && b.data ? (
              <TraceDiff a={a.data} b={b.data} />
            ) : (
              <p className="p-4 text-sm text-error">
                One of the traces could not be loaded — it may have aged out of retention.
              </p>
            )
          ) : a.isLoading ? (
            <CenteredSpinner />
          ) : a.error || !trace ? (
            <p className="p-4 text-sm text-error">
              Trace not found — it may have aged out of retention.
            </p>
          ) : (
            <>
              {view === "timeline" && (
                <Waterfall trace={trace} selectedSpanId={selectedSpanId} onSelectSpan={onSelectSpan} />
              )}
              {view === "spans" && (
                <SpansTable trace={trace} selectedSpanId={selectedSpanId} onSelectSpan={onSelectSpan} />
              )}
              {view === "flame" && (
                <Flamegraph trace={trace} selectedSpanId={selectedSpanId} onSelectSpan={onSelectSpan} />
              )}
              {view === "stats" && <TraceStats trace={trace} />}
              {view === "graph" && (
                <TraceTree
                  key={trace.traceId}
                  trace={trace}
                  selectedSpanId={selectedSpanId}
                  onSelectSpan={onSelectSpan}
                />
              )}
              {view === "json" && <TraceJson trace={trace} />}
            </>
          )}
        </div>

        {!comparing && selectedSpan && (
          <aside className="flex w-96 shrink-0 flex-col overflow-auto border-l border-neutral bg-base-100">
            <div className="flex items-center justify-between border-b border-neutral px-3 py-2">
              <span className="text-xs font-semibold">Span detail</span>
              <button
                aria-label="Close span detail"
                onClick={() => setMany({ span: undefined })}
                className="text-base-content/50 hover:text-base-content"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <SpanDetail span={selectedSpan} />
          </aside>
        )}
      </div>
    </div>
  );
}
