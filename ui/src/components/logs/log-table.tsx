"use client";

import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import { Download } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { CenteredSpinner, Spinner } from "@/components/ui/spinner";
import { SeverityBadge } from "./severity-badge";
import { formatTime, utcTooltip } from "@/lib/format";
import type { LogRecord } from "@/lib/api-types";

// A paste-friendly one-line rendering of a log record for the copy button.
function logLine(l: LogRecord): string {
  const parts = [l.timestamp, l.severity, l.service, l.body].filter(Boolean);
  return l.traceId ? `${parts.join(" ")} trace=${l.traceId}` : parts.join(" ");
}

// Identity of a row across renders. The timestamp alone repeats under load and
// the span id is absent on records that never joined a trace, so the two are
// combined with the position — the same key the table already renders by.
const rowKey = (l: LogRecord, i: number) => `${l.timestamp}-${l.spanId}-${i}`;

export function LogTable({
  pages,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  autoLoad = false,
  downloadName = "logs",
}: {
  pages?: LogRecord[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  // Fetch the next page when the end of the table scrolls into view. The
  // button stays for keyboards and for a viewport the sentinel never enters.
  autoLoad?: boolean;
  // Basename of the downloaded file — the subject these lines are about.
  downloadName?: string;
}) {
  const sentinel = useRef<HTMLDivElement>(null);
  // Which rows are picked, and the last one clicked so shift can extend from
  // it. Component state, not URL state: a selection is a gesture in progress,
  // and it should not survive a reload or be shared by a pasted link.
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string | null>(null);

  useEffect(() => {
    if (!autoLoad || !hasNextPage || isFetchingNextPage || !sentinel.current) return;
    const el = sentinel.current;
    const obs = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) fetchNextPage();
    });
    obs.observe(el);
    return () => obs.disconnect();
  }, [autoLoad, hasNextPage, isFetchingNextPage, fetchNextPage]);

  const logs = useMemo(() => pages?.flat() ?? [], [pages]);
  // What the copy and download controls act on: the picked rows when any are
  // picked, otherwise everything loaded. Both say their count out loud,
  // because "Copy" over a list that grows as you scroll is a lie about what
  // you are getting.
  const chosen = useMemo(
    () => (picked.size ? logs.filter((l, i) => picked.has(rowKey(l, i))) : logs),
    [logs, picked],
  );
  const text = useMemo(() => chosen.map(logLine).join("\n"), [chosen]);

  const click = (key: string, index: number, shift: boolean) => {
    const next = new Set(picked);
    if (shift && anchor !== null) {
      const from = logs.findIndex((l, i) => rowKey(l, i) === anchor);
      if (from >= 0) {
        const [lo, hi] = from < index ? [from, index] : [index, from];
        for (let i = lo; i <= hi; i++) next.add(rowKey(logs[i], i));
        setPicked(next);
        return;
      }
    }
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setPicked(next);
    setAnchor(key);
  };

  if (isLoading) return <CenteredSpinner />;
  if (!logs.length) {
    return (
      <Card className="p-8 text-center text-sm text-base-content/60">
        No logs match these filters in this window. Container stdout/stderr and
        OTLP logs appear here, correlated to traces by{" "}
        <code className="rounded bg-base-300 px-1">trace_id</code>.
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3" data-testid="log-table-actions">
        <CopyButton
          value={text}
          label={picked.size ? `Copy ${picked.size} selected` : `Copy ${logs.length} lines`}
          className="text-base-content/60"
        />
        <button
          type="button"
          onClick={() => downloadLog(text, downloadName)}
          className="inline-flex shrink-0 items-center gap-1 text-base-content/40 hover:text-base-content"
        >
          <Download className="h-3.5 w-3.5" aria-hidden />
          <span className="text-xs">Download .log</span>
        </button>
        {picked.size > 0 && (
          <button
            type="button"
            onClick={() => {
              setPicked(new Set());
              setAnchor(null);
            }}
            className="text-xs text-base-content/40 hover:text-base-content"
          >
            Clear selection
          </button>
        )}
      </div>

      <Card className="overflow-hidden">
        <table className="table-dense w-full text-sm">
          <thead>
            <tr className="border-b border-neutral text-left">
              <th className="w-8">
                <span className="sr-only">Select</span>
              </th>
              <th>Time</th>
              <th>Severity</th>
              <th>Service</th>
              <th>Message</th>
              <th className="text-right">Trace</th>
            </tr>
          </thead>
          <tbody>
            {logs.map((l, i) => {
              const key = rowKey(l, i);
              return (
                <tr
                  key={key}
                  className="group border-b border-neutral/40 align-top transition-colors last:border-0 hover:bg-base-300/50"
                >
                  <td>
                    <input
                      type="checkbox"
                      checked={picked.has(key)}
                      aria-label={`Select log line ${i + 1}`}
                      onClick={(e) => click(key, i, e.shiftKey)}
                      onChange={() => {}}
                      className="accent-primary"
                    />
                  </td>
                  <td className="whitespace-nowrap font-mono text-xs" title={utcTooltip(l.timestamp)}>
                    {formatTime(l.timestamp)}
                  </td>
                  <td>
                    <SeverityBadge severity={l.severity} />
                  </td>
                  <td className="whitespace-nowrap font-medium text-primary">{l.service}</td>
                  <td className="font-mono text-xs">
                    <span className="flex items-start gap-1">
                      <span className="min-w-0 break-all">{l.body}</span>
                      {/* Hidden until the row is hovered OR something in it
                          takes focus, so the control is reachable by keyboard
                          rather than by pointer alone. */}
                      <CopyButton
                        value={logLine(l)}
                        ariaLabel="Copy log line"
                        iconClass="h-3 w-3"
                        className="invisible mt-0.5 group-hover:visible group-focus-within:visible"
                      />
                    </span>
                  </td>
                  <td className="text-right">
                    {l.traceId ? (
                      <Link
                        href={`/traces?trace=${l.traceId}&tab=traces`}
                        className="font-mono text-xs text-primary hover:underline"
                        title="Open correlated trace"
                      >
                        {l.traceId.slice(0, 8)}
                      </Link>
                    ) : (
                      <span className="text-base-content/30">—</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {hasNextPage && (
          <div ref={sentinel} className="border-t border-neutral p-2 text-center">
            <Button variant="ghost" size="sm" onClick={fetchNextPage} disabled={isFetchingNextPage}>
              {isFetchingNextPage ? <Spinner className="h-4 w-4" /> : "Load more"}
            </Button>
          </div>
        )}
      </Card>
    </div>
  );
}

// The lines are already in the page, so the file is built here rather than
// asked of the hub: a round trip to be handed back bytes the browser is
// holding would only be a slower way to get the same text.
function downloadLog(text: string, name: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}-${new Date().toISOString().replace(/[:.]/g, "-")}.log`;
  a.click();
  URL.revokeObjectURL(url);
}
