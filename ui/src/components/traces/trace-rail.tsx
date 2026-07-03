"use client";

import { useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, GitCompare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CenteredSpinner, Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/cn";
import { formatAgo, formatMs } from "@/lib/format";
import { useURLState } from "@/hooks/use-url-state";
import type { TraceSummary } from "@/lib/api-types";

// Compact, collapsible trace list for the split workspace (SkyWalking's left
// rail). Stays live so switching traces is one click; each row can be pinned as
// the comparison target.
export function TraceRail({
  pages,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  selectedTraceId,
  compareId,
}: {
  pages?: TraceSummary[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  selectedTraceId?: string;
  compareId?: string | null;
}) {
  const { setMany } = useURLState();
  const [collapsed, setCollapsed] = useState(false);

  const traces = useMemo(() => pages?.flat() ?? [], [pages]);
  const maxDur = useMemo(
    () => Math.max(1, ...traces.map((t) => t.durationMs)),
    [traces],
  );

  if (collapsed) {
    return (
      <div className="flex w-9 shrink-0 flex-col items-center rounded-xl border border-neutral bg-base-200 py-2">
        <button
          aria-label="Expand trace list"
          onClick={() => setCollapsed(false)}
          className="text-base-content/60 hover:text-base-content"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
        <span className="mt-2 text-[10px] uppercase tracking-wider text-base-content/50 [writing-mode:vertical-rl]">
          Traces · {traces.length}
        </span>
      </div>
    );
  }

  return (
    <div className="flex w-72 shrink-0 flex-col overflow-hidden rounded-xl border border-neutral bg-base-200">
      <div className="flex items-center justify-between border-b border-neutral px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-base-content/60">
          Traces · {traces.length}
        </span>
        <button
          aria-label="Collapse trace list"
          onClick={() => setCollapsed(true)}
          className="text-base-content/50 hover:text-base-content"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {isLoading ? (
          <CenteredSpinner />
        ) : traces.length === 0 ? (
          <p className="p-4 text-center text-xs text-base-content/60">No traces.</p>
        ) : (
          traces.map((t) => {
            const isSel = t.traceId === selectedTraceId;
            const isCmp = t.traceId === compareId;
            const isError = t.errorCount > 0;
            return (
              <div
                key={t.traceId}
                onClick={() => setMany({ trace: t.traceId, span: undefined })}
                className={cn(
                  "group/row relative cursor-pointer border-b border-neutral/40 px-3 py-2 pr-8",
                  isSel ? "bg-primary/10" : "hover:bg-base-300/40",
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate font-mono text-xs">{t.rootOperation}</span>
                  <span
                    className={cn(
                      "shrink-0 font-mono text-[10px]",
                      isError ? "text-error" : "text-success",
                    )}
                  >
                    {isError ? "ERR" : "OK"}
                  </span>
                </div>
                <div className="mt-1 h-1 w-full overflow-hidden rounded bg-base-300">
                  <div
                    className={cn("h-full rounded", isError ? "bg-error" : "bg-primary")}
                    style={{ width: `${Math.max((t.durationMs / maxDur) * 100, 2)}%` }}
                  />
                </div>
                <div className="mt-1 flex items-center justify-between gap-2 text-[10px] text-base-content/50">
                  <span className="truncate">{t.rootService}</span>
                  <span className="shrink-0">
                    {formatMs(t.durationMs)} · {formatAgo(t.startTime)}
                  </span>
                </div>
                <button
                  aria-label="Compare with this trace"
                  title="Compare with this trace"
                  onClick={(e) => {
                    e.stopPropagation();
                    setMany({ compare: isCmp ? undefined : t.traceId });
                  }}
                  className={cn(
                    "absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 transition-opacity",
                    isCmp
                      ? "text-primary opacity-100"
                      : "text-base-content/40 opacity-0 hover:text-base-content group-hover/row:opacity-100",
                  )}
                >
                  <GitCompare className="h-3.5 w-3.5" />
                </button>
              </div>
            );
          })
        )}
        {hasNextPage && (
          <div className="p-2 text-center">
            <Button
              variant="ghost"
              size="sm"
              onClick={fetchNextPage}
              disabled={isFetchingNextPage}
            >
              {isFetchingNextPage ? <Spinner className="h-4 w-4" /> : "Load more"}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
