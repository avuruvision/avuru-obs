"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import {
  DEFAULT_PRESET,
  TIME_RANGE_KEY,
  parseSelection,
  selectionFromParams,
  serializeSelection,
  useTimeRange,
  writeSelection,
} from "@/hooks/use-time-range";

// Keeps the global time range sticky across navigation, mirroring
// ProjectProvider: sidebar links are bare paths, so `?range=` is dropped on
// every page change — localStorage carries the value over and this component
// writes it back into the URL. Mounted once in Providers (the per-page Topbar
// picker is Suspense-wrapped and not a reliable effect host).
export function TimeRangeSync({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { selection } = useTimeRange();

  // A shared link's ?range= must stick: mirror what the URL says into
  // localStorage so the next in-app navigation stays on the same range. Only
  // what the URL says: on a fresh page load the first commit still holds the
  // server snapshot (the default), and mirroring that would wipe the stored
  // range before the client snapshot could read it.
  useEffect(() => {
    const fromURL = selectionFromParams(new URLSearchParams(window.location.search));
    if (!fromURL) return;
    const serialized = serializeSelection(fromURL);
    if (localStorage.getItem(TIME_RANGE_KEY) !== serialized) {
      localStorage.setItem(TIME_RANGE_KEY, serialized);
    }
  }, [selection]);

  // Re-materialize ?range= (and an absolute window's ?from=/?to=) after
  // navigations, keeping links shareable (default is deliberately unmarked).
  useEffect(() => {
    if (selection === DEFAULT_PRESET) return;
    const sel = parseSelection(selection);
    if (!sel) return;
    const params = new URLSearchParams(window.location.search);
    const current = selectionFromParams(params);
    if (current && serializeSelection(current) === selection) return;
    writeSelection(params, sel);
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
  }, [pathname, selection]);

  return children;
}
