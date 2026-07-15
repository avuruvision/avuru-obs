"use client";

import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { TimeParams } from "@/lib/query-keys";

export const RANGE_PRESETS = {
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "24h": 24 * 60 * 60_000,
} as const;

export type RangePreset = keyof typeof RANGE_PRESETS;

export const DEFAULT_PRESET: RangePreset = "15m";

export const TIME_RANGE_KEY = "avuru-time-range";
export const TIME_RANGE_CHANGE_EVENT = "avuru-time-range-change";

function isPreset(v: string | null): v is RangePreset {
  return v !== null && v in RANGE_PRESETS;
}

// Same doctrine as project-context.tsx: URL `?range=` is the shareable truth,
// localStorage bridges navigations that drop the param (sidebar links are
// bare paths), and the default stays out of the URL. NO useSearchParams —
// window.location.search is ground truth in the static export (see
// use-url-state.ts); useSyncExternalStore keeps consumers in step.
function subscribe(callback: () => void) {
  window.addEventListener("popstate", callback);
  window.addEventListener("storage", callback);
  window.addEventListener(TIME_RANGE_CHANGE_EVENT, callback);
  return () => {
    window.removeEventListener("popstate", callback);
    window.removeEventListener("storage", callback);
    window.removeEventListener(TIME_RANGE_CHANGE_EVENT, callback);
  };
}

function snapshot(): RangePreset {
  const fromURL = new URLSearchParams(window.location.search).get("range");
  if (isPreset(fromURL)) return fromURL;
  const stored = localStorage.getItem(TIME_RANGE_KEY);
  if (isPreset(stored)) return stored;
  return DEFAULT_PRESET;
}

// URL/localStorage ⇄ {start,end}: start/end are computed at render; TanStack
// Query's staleTime debounces the "now" drift between refetches.
export function useTimeRange() {
  const preset = useSyncExternalStore(subscribe, snapshot, () => DEFAULT_PRESET);

  // Native replaceState, not router.replace — see useURLState for why.
  const setPreset = useCallback((p: RangePreset) => {
    localStorage.setItem(TIME_RANGE_KEY, p);
    const params = new URLSearchParams(window.location.search);
    if (p === DEFAULT_PRESET) params.delete("range");
    else params.set("range", p);
    const qs = params.toString();
    window.history.replaceState(
      null,
      "",
      qs ? `${window.location.pathname}?${qs}` : window.location.pathname,
    );
    window.dispatchEvent(new Event(TIME_RANGE_CHANGE_EVENT));
  }, []);

  const time: TimeParams = useMemo(() => {
    const end = new Date();
    const start = new Date(end.getTime() - RANGE_PRESETS[preset]);
    return { start: start.toISOString(), end: end.toISOString() };
  }, [preset]);

  return { preset, setPreset, time, windowMs: RANGE_PRESETS[preset] };
}
