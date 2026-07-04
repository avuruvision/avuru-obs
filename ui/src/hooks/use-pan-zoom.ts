"use client";

// Pan/zoom for a CSS-transformed content layer: wheel zooms about the
// cursor, pointer drag pans, fit() centers given content bounds. Only the
// transform mutates (compositor-only), so hundreds of child nodes stay cheap.

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";

const K_MIN = 0.2;
const K_MAX = 2.5;
const FIT_PAD = 24;

export interface PanZoomTransform {
  x: number;
  y: number;
  k: number;
}

const clampK = (k: number) => Math.min(Math.max(k, K_MIN), K_MAX);

const DRAG_THRESHOLD = 4;

export function usePanZoom(containerRef: RefObject<HTMLElement | null>) {
  const [transform, setTransform] = useState<PanZoomTransform>({ x: 0, y: 0, k: 1 });
  const drag = useRef<{
    id: number;
    startX: number;
    startY: number;
    x: number;
    y: number;
    active: boolean;
  } | null>(null);

  // The wheel listener must be non-passive: without preventDefault the page
  // scrolls instead of the canvas zooming.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const cx = e.clientX - rect.left;
      const cy = e.clientY - rect.top;
      setTransform((prev) => {
        const k = clampK(prev.k * Math.exp(-e.deltaY * 0.0015));
        // Zoom about the cursor: keep the content point under it fixed.
        return { k, x: cx - ((cx - prev.x) * k) / prev.k, y: cy - ((cy - prev.y) * k) / prev.k };
      });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [containerRef]);

  const onPointerDown = useCallback((e: React.PointerEvent<HTMLElement>) => {
    drag.current = {
      id: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      x: e.clientX,
      y: e.clientY,
      active: false,
    };
  }, []);

  const onPointerMove = useCallback((e: React.PointerEvent<HTMLElement>) => {
    const d = drag.current;
    if (!d || e.pointerId !== d.id) return;
    if (!d.active) {
      if (
        Math.abs(e.clientX - d.startX) + Math.abs(e.clientY - d.startY) <
        DRAG_THRESHOLD
      ) {
        return;
      }
      // Capture only once an actual drag starts: capturing on pointerdown
      // would retarget the click to the container and node cards would
      // never receive it.
      d.active = true;
      e.currentTarget.setPointerCapture(d.id);
    }
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    d.x = e.clientX;
    d.y = e.clientY;
    setTransform((prev) => ({ ...prev, x: prev.x + dx, y: prev.y + dy }));
  }, []);

  const onPointerUp = useCallback(() => {
    drag.current = null;
  }, []);

  const zoomBy = useCallback(
    (factor: number) => {
      const el = containerRef.current;
      const cx = el ? el.clientWidth / 2 : 0;
      const cy = el ? el.clientHeight / 2 : 0;
      setTransform((prev) => {
        const k = clampK(prev.k * factor);
        return { k, x: cx - ((cx - prev.x) * k) / prev.k, y: cy - ((cy - prev.y) * k) / prev.k };
      });
    },
    [containerRef],
  );

  const fit = useCallback(
    (contentW: number, contentH: number) => {
      const el = containerRef.current;
      if (!el || contentW <= 0 || contentH <= 0) return;
      const k = clampK(
        Math.min(
          (el.clientWidth - FIT_PAD * 2) / contentW,
          (el.clientHeight - FIT_PAD * 2) / contentH,
          1,
        ),
      );
      setTransform({
        k,
        x: Math.max((el.clientWidth - contentW * k) / 2, FIT_PAD),
        y: Math.max((el.clientHeight - contentH * k) / 2, FIT_PAD),
      });
    },
    [containerRef],
  );

  return {
    transform,
    handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel: onPointerUp },
    zoomIn: () => zoomBy(1.25),
    zoomOut: () => zoomBy(0.8),
    fit,
  };
}
