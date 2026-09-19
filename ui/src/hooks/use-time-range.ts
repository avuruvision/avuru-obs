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
// The ?range= value that says the window is the absolute ?from=/?to= pair
// beside it rather than a preset.
export const CUSTOM_RANGE = "custom";

export const TIME_RANGE_KEY = "avuru-time-range";
export const TIME_RANGE_CHANGE_EVENT = "avuru-time-range-change";

// An absolute window, both ends RFC 3339 — what the hub's start/end parse.
export interface CustomRange {
  from: string;
  to: string;
}

export function isPreset(v: string | null | undefined): v is RangePreset {
  return v !== null && v !== undefined && v in RANGE_PRESETS;
}

// The selection as one string, so useSyncExternalStore can compare snapshots
// by value: a preset is its own name; an absolute window is
// "custom|<from>|<to>". The same string is what localStorage holds.
export function serializeSelection(sel: RangePreset | CustomRange): string {
  return typeof sel === "string" ? sel : `${CUSTOM_RANGE}|${sel.from}|${sel.to}`;
}

export function parseSelection(raw: string | null | undefined): RangePreset | CustomRange | null {
  if (isPreset(raw)) return raw;
  if (!raw?.startsWith(`${CUSTOM_RANGE}|`)) return null;
  const [, from, to] = raw.split("|");
  return validCustom(from, to);
}

function validCustom(from?: string | null, to?: string | null): CustomRange | null {
  if (!from || !to) return null;
  const a = Date.parse(from);
  const b = Date.parse(to);
  if (Number.isNaN(a) || Number.isNaN(b) || b <= a) return null;
  return { from: new Date(a).toISOString(), to: new Date(b).toISOString() };
}

// Reads the selection out of a query string: a preset, or custom with its two
// ends. An unparseable pair is no selection, so the caller falls through.
export function selectionFromParams(params: URLSearchParams): RangePreset | CustomRange | null {
  const range = params.get("range");
  if (isPreset(range)) return range;
  if (range === CUSTOM_RANGE) return validCustom(params.get("from"), params.get("to"));
  return null;
}

// Writes the selection into a query string, the way the picker and the
// navigation sync both spell it: the default stays out of the URL, and an
// absolute window carries its two ends.
export function writeSelection(params: URLSearchParams, sel: RangePreset | CustomRange) {
  params.delete("from");
  params.delete("to");
  if (typeof sel === "string") {
    if (sel === DEFAULT_PRESET) params.delete("range");
    else params.set("range", sel);
  } else {
    params.set("range", CUSTOM_RANGE);
    params.set("from", sel.from);
    params.set("to", sel.to);
  }
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

function snapshot(): string {
  const fromURL = selectionFromParams(new URLSearchParams(window.location.search));
  if (fromURL) return serializeSelection(fromURL);
  const stored = parseSelection(localStorage.getItem(TIME_RANGE_KEY));
  if (stored) return serializeSelection(stored);
  return DEFAULT_PRESET;
}

// URL/localStorage ⇄ {start,end}: a preset's start/end are computed at render;
// TanStack Query's staleTime debounces the "now" drift between refetches. An
// absolute window is what it says.
export function useTimeRange() {
  const selection = useSyncExternalStore(subscribe, snapshot, () => DEFAULT_PRESET);
  const parsed = useMemo(() => parseSelection(selection) ?? DEFAULT_PRESET, [selection]);
  const custom = typeof parsed === "string" ? null : parsed;
  const preset: RangePreset = typeof parsed === "string" ? parsed : DEFAULT_PRESET;

  // Native replaceState, not router.replace — see useURLState for why.
  const select = useCallback((sel: RangePreset | CustomRange) => {
    localStorage.setItem(TIME_RANGE_KEY, serializeSelection(sel));
    const params = new URLSearchParams(window.location.search);
    writeSelection(params, sel);
    const qs = params.toString();
    window.history.replaceState(
      null,
      "",
      qs ? `${window.location.pathname}?${qs}` : window.location.pathname,
    );
    window.dispatchEvent(new Event(TIME_RANGE_CHANGE_EVENT));
  }, []);
  const setPreset = useCallback((p: RangePreset) => select(p), [select]);
  // Rejects a window that does not run forwards; says so by returning false.
  const setCustom = useCallback((from: string, to: string) => {
    const range = validCustom(from, to);
    if (!range) return false;
    select(range);
    return true;
  }, [select]);

  const time: TimeParams = useMemo(() => {
    if (custom) return { start: custom.from, end: custom.to };
    const end = new Date();
    const start = new Date(end.getTime() - RANGE_PRESETS[preset]);
    return { start: start.toISOString(), end: end.toISOString() };
  }, [preset, custom]);
  const windowMs = custom
    ? Date.parse(custom.to) - Date.parse(custom.from)
    : RANGE_PRESETS[preset];

  return { preset, setPreset, custom, setCustom, selection, time, windowMs };
}
