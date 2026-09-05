"use client";

import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";

// Anchored layers — the option list of a Select or Combobox, the panel of a
// DropdownMenu — used to be `position: absolute` next to their trigger. That
// works only while no ancestor clips: every settings Card carries
// `overflow-hidden`, so the list was painted and immediately cut off, leaving a
// control that opened and could not be chosen from. Positioning `fixed` against
// the trigger's viewport rect, in a portal on <body>, takes the layer out of
// every clipping and stacking context there is.
//
// The trade of fixed positioning is that the rect goes stale the moment
// anything scrolls, so the layer re-measures on scroll and resize while open,
// and flips above the trigger when the space below cannot hold it.

export interface AnchoredLayerOptions {
  open: boolean;
  // Called on a click outside both trigger and layer. Escape is the caller's
  // to handle: it belongs with the rest of its keyboard map.
  onDismiss: () => void;
  align?: "start" | "end";
  // "trigger" locks the layer to the trigger's width (a select field must line
  // up with its box); "min" uses it as a floor; "auto" leaves width to CSS, for
  // a menu hung off an icon button too narrow to size anything.
  width?: "trigger" | "min" | "auto";
  desiredHeight?: number;
}

const GAP = 4;
const EDGE = 8;
const MIN_HEIGHT = 120;

export function useAnchoredLayer<A extends HTMLElement, L extends HTMLElement>({
  open,
  onDismiss,
  align = "start",
  width = "trigger",
  desiredHeight = 240,
}: AnchoredLayerOptions) {
  const anchorRef = useRef<A>(null);
  const layerRef = useRef<L>(null);
  const [style, setStyle] = useState<CSSProperties | null>(null);

  const measure = useCallback((): CSSProperties | null => {
    const el = anchorRef.current;
    if (!el) return null;
    const r = el.getBoundingClientRect();
    const below = window.innerHeight - r.bottom - GAP - EDGE;
    const above = r.top - GAP - EDGE;
    const flip = below < Math.min(desiredHeight, MIN_HEIGHT) && above > below;
    const next: CSSProperties = {
      position: "fixed",
      maxHeight: Math.max(MIN_HEIGHT, Math.floor(flip ? above : below)),
      ...(width === "trigger"
        ? { width: r.width }
        : width === "min"
          ? { minWidth: r.width }
          : {}),
    };
    if (align === "end") {
      next.right = Math.max(EDGE, window.innerWidth - r.right);
    } else {
      next.left = Math.max(EDGE, r.left);
    }
    if (flip) {
      next.bottom = window.innerHeight - r.top + GAP;
    } else {
      next.top = r.bottom + GAP;
    }
    return next;
  }, [align, width, desiredHeight]);

  // Callers measure through this BEFORE flipping `open`, so the layer never
  // paints at a stale position and jumps: both state updates land in the same
  // event handler and React renders them together. That is also why this
  // effect only subscribes and never measures — a measure here would be a
  // second render for a position already known.
  const reposition = useCallback(() => setStyle(measure()), [measure]);

  useEffect(() => {
    if (!open) return;
    const onScrollOrResize = () => reposition();
    // Capture: a scroll inside any ancestor moves the trigger too, and those
    // events do not bubble to window.
    window.addEventListener("scroll", onScrollOrResize, true);
    window.addEventListener("resize", onScrollOrResize);
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (anchorRef.current?.contains(t) || layerRef.current?.contains(t)) return;
      onDismiss();
    };
    document.addEventListener("mousedown", onDown);
    return () => {
      window.removeEventListener("scroll", onScrollOrResize, true);
      window.removeEventListener("resize", onScrollOrResize);
      document.removeEventListener("mousedown", onDown);
    };
  }, [open, onDismiss, reposition]);

  return { anchorRef, layerRef, style, reposition };
}
