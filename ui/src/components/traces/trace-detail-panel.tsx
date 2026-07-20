"use client";

import { useRef, useState } from "react";
import { GitCompare, Maximize2, Minimize2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { cn } from "@/lib/cn";
import { useLocalStorageNumber } from "@/hooks/use-local-storage-number";
import { useTrace } from "@/hooks/use-traces-data";
import { useURLState } from "@/hooks/use-url-state";
import type { Span } from "@/lib/api-types";
import { Waterfall } from "./waterfall";
import { SpanDetail } from "./span-detail";
import { SpanDetailOverlay } from "./span-detail-overlay";
import { TraceSummaryBar } from "./trace-summary-bar";
import { SpansTable } from "./views/spans-table";
import { Flamegraph } from "./views/flamegraph";
import { TraceStats } from "./views/trace-stats";
import { TraceTree } from "./views/trace-tree";
import { TraceJson } from "./views/trace-json";
import { TraceDiff } from "./views/trace-diff";
import { CLEAR_WORKSPACE_PARAMS } from "./workspace-params";

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

const SPAN_PANEL_WIDTH_KEY = "avuru-span-detail-width";
const SPAN_PANEL_DEFAULT = 384; // matches the old w-96
const SPAN_PANEL_MIN = 320;

// Draggable divider for the span-detail aside. Pointer capture keeps the drag
// alive when the cursor leaves the 6px hit area.
function ResizeHandle({
  onDrag,
  onNudge,
  onReset,
}: {
  onDrag: (clientX: number) => void;
  onNudge: (deltaPx: number) => void;
  onReset: () => void;
}) {
  const dragging = useRef(false);
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize span detail"
      tabIndex={0}
      onPointerDown={(e) => {
        e.preventDefault();
        dragging.current = true;
        e.currentTarget.setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => dragging.current && onDrag(e.clientX)}
      onPointerUp={(e) => {
        dragging.current = false;
        e.currentTarget.releasePointerCapture(e.pointerId);
      }}
      onDoubleClick={onReset}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") {
          e.preventDefault();
          onNudge(16);
        }
        if (e.key === "ArrowRight") {
          e.preventDefault();
          onNudge(-16);
        }
      }}
      className="w-1.5 shrink-0 cursor-col-resize touch-none bg-transparent transition-colors hover:bg-primary/40 focus-visible:bg-primary/40"
    />
  );
}

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

  const bodyRef = useRef<HTMLDivElement>(null);
  const [panelWidth, setPanelWidth] = useLocalStorageNumber(
    SPAN_PANEL_WIDTH_KEY,
    SPAN_PANEL_DEFAULT,
  );
  const [expanded, setExpanded] = useState(false);

  // Clamp to [320px, 70% of the workspace body], measured live during drag.
  const clampWidth = (w: number) => {
    const body = bodyRef.current;
    const max = body
      ? Math.max(SPAN_PANEL_MIN, Math.round(body.getBoundingClientRect().width * 0.7))
      : SPAN_PANEL_DEFAULT;
    return Math.min(Math.max(Math.round(w), SPAN_PANEL_MIN), max);
  };

  const comparing = Boolean(compareId);
  const view = (VIEWS.find((v) => v.value === get("view"))?.value ?? "timeline") as View;
  const selectedSpanId = get("span") ?? null;

  const trace = a.data;
  const selectedSpan = trace?.spans.find((s) => s.spanId === selectedSpanId) ?? null;

  // ?focus= dims other services' spans; inert when the focused service isn't
  // in this trace (focus survives trace switches and applies where relevant).
  const focusRaw = get("focus");
  const focusService =
    focusRaw && trace?.spans.some((s) => s.service === focusRaw) ? focusRaw : null;

  const close = () => setMany(CLEAR_WORKSPACE_PARAMS);
  const onSelectSpan = (span: Span) =>
    setMany({ span: span.spanId === selectedSpanId ? undefined : span.spanId });

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-neutral bg-base-200">
      <header className="flex flex-wrap items-center gap-2 border-b border-neutral px-3 py-2">
        <span className="group inline-flex min-w-0 items-center gap-1">
          <span className="truncate font-mono text-xs font-semibold">{traceId}</span>
          <CopyButton
            value={traceId}
            ariaLabel="Copy trace id"
            iconClass="h-3 w-3"
            className="invisible group-hover:visible"
          />
        </span>

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

      <div ref={bodyRef} className="flex min-h-0 flex-1">
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
                <Waterfall
                  trace={trace}
                  selectedSpanId={selectedSpanId}
                  focusService={focusService}
                  onSelectSpan={onSelectSpan}
                />
              )}
              {view === "spans" && (
                <SpansTable
                  trace={trace}
                  selectedSpanId={selectedSpanId}
                  focusService={focusService}
                  onSelectSpan={onSelectSpan}
                />
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
          <>
            <ResizeHandle
              onDrag={(clientX) => {
                const body = bodyRef.current;
                if (body)
                  setPanelWidth(clampWidth(body.getBoundingClientRect().right - clientX));
              }}
              onNudge={(d) => setPanelWidth(clampWidth(panelWidth + d))}
              onReset={() => setPanelWidth(SPAN_PANEL_DEFAULT)}
            />
            <aside
              style={{ width: panelWidth }}
              className="flex min-w-80 max-w-[70%] shrink-0 flex-col overflow-auto border-l border-neutral bg-base-100"
            >
              <div className="flex items-center justify-between border-b border-neutral px-3 py-2">
                <span className="text-xs font-semibold">Span detail</span>
                <div className="flex items-center gap-1.5">
                  <button
                    aria-label="Expand span detail"
                    onClick={() => setExpanded(true)}
                    className="text-base-content/50 hover:text-base-content"
                  >
                    <Maximize2 className="h-4 w-4" />
                  </button>
                  <button
                    aria-label="Close span detail"
                    onClick={() => setMany({ span: undefined })}
                    className="text-base-content/50 hover:text-base-content"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              </div>
              <SpanDetail span={selectedSpan} />
            </aside>
            {expanded && (
              <SpanDetailOverlay span={selectedSpan} onClose={() => setExpanded(false)} />
            )}
          </>
        )}
      </div>
    </div>
  );
}
