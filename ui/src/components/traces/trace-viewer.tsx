"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Link2, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { cn } from "@/lib/cn";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
import { useTrace } from "@/hooks/use-traces-data";
import { useURLState } from "@/hooks/use-url-state";
import { Waterfall } from "./waterfall";
import { SpansTable } from "./views/spans-table";
import { Flamegraph } from "./views/flamegraph";
import { TraceStats } from "./views/trace-stats";
import { TraceGraph } from "./views/trace-graph";
import { TraceJson } from "./views/trace-json";

const VIEWS = [
  { value: "timeline", label: "Timeline" },
  { value: "spans", label: "Spans" },
  { value: "flame", label: "Flamegraph" },
  { value: "stats", label: "Statistics" },
  { value: "graph", label: "Graph" },
  { value: "json", label: "JSON" },
] as const;

type View = (typeof VIEWS)[number]["value"];

// Full-window trace explorer (Jaeger-style). Opened via ?trace=<id>; the active
// view persists in ?view= so a deep link is shareable. Rendered as a portal so
// it covers the whole app chrome.
export function TraceViewer({ traceId, onClose }: { traceId: string; onClose: () => void }) {
  const { get, setMany } = useURLState();
  const { data, isLoading, error } = useTrace(traceId);
  const [copied, setCopied] = useState(false);

  const view = (VIEWS.find((v) => v.value === get("view"))?.value ?? "timeline") as View;

  // Esc closes; lock body scroll while open.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard blocked — no-op
    }
  };

  // Portals need the DOM; the page is statically prerendered, so bail on server.
  if (typeof document === "undefined") return null;

  const errorCount = data?.spans.filter((s) => s.statusCode === "Error").length ?? 0;

  return createPortal(
    <div className="fixed inset-0 z-50 flex flex-col bg-base-100">
      <header className="flex flex-wrap items-center gap-3 border-b border-neutral px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-sm font-semibold">{traceId}</span>
          {data && (
            <>
              <Badge tone="primary">{data.spans.length} spans</Badge>
              <Badge tone="neutral">{formatMs(data.durationMs)}</Badge>
              {errorCount > 0 && <Badge tone="error">{errorCount} errors</Badge>}
              <span
                className="hidden text-xs text-base-content/50 sm:inline"
                title={utcTooltip(data.startTime)}
              >
                {formatTime(data.startTime)}
              </span>
            </>
          )}
        </div>

        <div className="ml-auto flex items-center gap-2">
          <div className="flex overflow-hidden rounded-lg border border-neutral">
            {VIEWS.map((v) => (
              <button
                key={v.value}
                onClick={() => setMany({ view: v.value === "timeline" ? undefined : v.value })}
                className={cn(
                  "px-2.5 py-1 text-xs font-medium transition-colors",
                  v.value === view
                    ? "bg-primary text-primary-content"
                    : "text-base-content/60 hover:bg-base-300 hover:text-base-content",
                )}
              >
                {v.label}
              </button>
            ))}
          </div>
          <Button variant="ghost" size="sm" onClick={copyLink}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Link2 className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">{copied ? "Copied" : "Link"}</span>
          </Button>
          <Button variant="ghost" size="icon" aria-label="Close trace (Esc)" onClick={onClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
      </header>

      <main className="flex-1 overflow-auto p-4">
        {isLoading && <CenteredSpinner />}
        {error && (
          <p className="p-4 text-sm text-error">
            Trace not found — it may have aged out of retention.
          </p>
        )}
        {data && (
          <>
            {view === "timeline" && <Waterfall trace={data} />}
            {view === "spans" && <SpansTable trace={data} />}
            {view === "flame" && <Flamegraph trace={data} />}
            {view === "stats" && <TraceStats trace={data} />}
            {view === "graph" && <TraceGraph trace={data} />}
            {view === "json" && <TraceJson trace={data} />}
          </>
        )}
      </main>
    </div>,
    document.body,
  );
}
