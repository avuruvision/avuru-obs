"use client";

import { useCallback } from "react";
import { usePathname, useSearchParams } from "next/navigation";

// Filter/selection state lives in the URL (shareable links are a product
// feature — agent_docs/ui_patterns.md). Detail views use query params, never
// dynamic routes (static export).
//
// Writes go through native history.replaceState (Next syncs useSearchParams
// from it), NOT router.replace: in the static export the router transition can
// refuse to commit a query-only change and re-syncs the old URL, and its
// searchParams snapshot can go stale. window.location.search is ground truth.
export function useURLState() {
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const get = useCallback(
    (key: string) => searchParams.get(key) ?? undefined,
    [searchParams],
  );

  const setMany = useCallback(
    (entries: Record<string, string | undefined>) => {
      const params = new URLSearchParams(window.location.search);
      for (const [k, v] of Object.entries(entries)) {
        if (v === undefined || v === "") params.delete(k);
        else params.set(k, v);
      }
      const qs = params.toString();
      window.history.replaceState(null, "", qs ? `${pathname}?${qs}` : pathname);
    },
    [pathname],
  );

  return { get, setMany };
}
