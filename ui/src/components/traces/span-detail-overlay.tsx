"use client";

import { useEffect } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { Span } from "@/lib/api-types";
import { SpanDetail } from "./span-detail";

// Full-size zoom of the span detail, portalled above the fullscreen layer
// (z-50). Esc is handled in the CAPTURE phase with stopImmediatePropagation so
// it closes only the overlay — trace-workspace also listens for Esc on
// document to exit fullscreen, and must not fire while the overlay is open.
export function SpanDetailOverlay({ span, onClose }: { span: Span; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopImmediatePropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  if (typeof document === "undefined") return null;
  return createPortal(
    <div
      className="fixed inset-0 z-60 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Span detail expanded"
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[85vh] w-[min(1100px,92vw)] flex-col overflow-hidden rounded-xl border border-neutral bg-base-100 shadow-xl"
      >
        <div className="flex items-center justify-between border-b border-neutral px-4 py-2">
          <span className="truncate font-mono text-xs font-semibold">{span.operation}</span>
          <button
            aria-label="Close expanded span detail"
            onClick={onClose}
            className="text-base-content/50 hover:text-base-content"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="min-h-0 overflow-auto">
          <SpanDetail span={span} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
