"use client";

import { useEffect } from "react";
import { createPortal } from "react-dom";
import { useURLState } from "@/hooks/use-url-state";
import { TraceRail } from "./trace-rail";
import { TraceDetailPanel } from "./trace-detail-panel";
import type { TraceSummary } from "@/lib/api-types";

// SkyWalking-style split workspace: trace list rail (left) ↔ detail (right). The
// same content renders inline (default) or in a full-window portal (?full=1).
export function TraceWorkspace({
  traceId,
  compareId,
  pages,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
}: {
  traceId: string;
  compareId?: string | null;
  pages?: TraceSummary[][];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
}) {
  const { get, setMany } = useURLState();
  const fullscreen = get("full") === "1";

  // In full-window mode, Esc steps back to the inline split and the body scroll
  // is locked.
  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMany({ full: undefined });
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [fullscreen, setMany]);

  const inner = (
    <div className="flex h-full min-h-0 gap-3">
      <TraceRail
        pages={pages}
        isLoading={isLoading}
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        fetchNextPage={fetchNextPage}
        selectedTraceId={traceId}
        compareId={compareId}
      />
      <TraceDetailPanel traceId={traceId} compareId={compareId} fullscreen={fullscreen} />
    </div>
  );

  if (fullscreen) {
    if (typeof document === "undefined") return null;
    return createPortal(
      <div className="fixed inset-0 z-50 bg-base-100 p-3">{inner}</div>,
      document.body,
    );
  }

  return <div className="h-[72vh]">{inner}</div>;
}
