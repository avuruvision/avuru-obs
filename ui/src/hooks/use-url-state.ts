"use client";

import { useCallback } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

// Filter/selection state lives in the URL (shareable links are a product
// feature — agent_docs/ui_patterns.md). Detail views use query params, never
// dynamic routes (static export).
export function useURLState() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const get = useCallback(
    (key: string) => searchParams.get(key) ?? undefined,
    [searchParams],
  );

  const setMany = useCallback(
    (entries: Record<string, string | undefined>) => {
      const params = new URLSearchParams(searchParams);
      for (const [k, v] of Object.entries(entries)) {
        if (v === undefined || v === "") params.delete(k);
        else params.set(k, v);
      }
      router.replace(`${pathname}?${params.toString()}`, { scroll: false });
    },
    [router, pathname, searchParams],
  );

  return { get, setMany };
}
