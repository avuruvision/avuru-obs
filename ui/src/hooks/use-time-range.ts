"use client";

import { useCallback, useMemo } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type { TimeParams } from "@/lib/query-keys";

export const RANGE_PRESETS = {
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "24h": 24 * 60 * 60_000,
} as const;

export type RangePreset = keyof typeof RANGE_PRESETS;

const DEFAULT_PRESET: RangePreset = "15m";

function isPreset(v: string | null): v is RangePreset {
  return v !== null && v in RANGE_PRESETS;
}

// URL ⇄ {start,end}: the range lives in ?range= so links stay shareable.
// start/end are computed at render; TanStack Query's staleTime debounces the
// "now" drift between refetches.
export function useTimeRange() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const raw = searchParams.get("range");
  const preset: RangePreset = isPreset(raw) ? raw : DEFAULT_PRESET;

  const setPreset = useCallback(
    (p: RangePreset) => {
      const params = new URLSearchParams(searchParams);
      params.set("range", p);
      router.replace(`${pathname}?${params.toString()}`);
    },
    [router, pathname, searchParams],
  );

  const time: TimeParams = useMemo(() => {
    const end = new Date();
    const start = new Date(end.getTime() - RANGE_PRESETS[preset]);
    return { start: start.toISOString(), end: end.toISOString() };
  }, [preset]);

  return { preset, setPreset, time, windowMs: RANGE_PRESETS[preset] };
}
