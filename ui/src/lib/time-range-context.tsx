"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import {
  DEFAULT_PRESET,
  TIME_RANGE_KEY,
  useTimeRange,
} from "@/hooks/use-time-range";

// Keeps the global time range sticky across navigation, mirroring
// ProjectProvider: sidebar links are bare paths, so `?range=` is dropped on
// every page change — localStorage carries the value over and this component
// writes it back into the URL. Mounted once in Providers (the per-page Topbar
// picker is Suspense-wrapped and not a reliable effect host).
export function TimeRangeSync({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { preset } = useTimeRange();

  // A shared link's ?range= must stick: mirror the resolved preset into
  // localStorage so the next in-app navigation stays on the same range.
  useEffect(() => {
    if (localStorage.getItem(TIME_RANGE_KEY) !== preset) {
      localStorage.setItem(TIME_RANGE_KEY, preset);
    }
  }, [preset]);

  // Re-materialize ?range= after navigations, keeping links shareable
  // (default is deliberately unmarked).
  useEffect(() => {
    if (preset === DEFAULT_PRESET) return;
    const params = new URLSearchParams(window.location.search);
    if (params.get("range") === preset) return;
    params.set("range", preset);
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
  }, [pathname, preset]);

  return children;
}
