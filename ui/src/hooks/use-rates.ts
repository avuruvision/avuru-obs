"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPut } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { RatesResponse, RateTable } from "@/lib/api-types";

// The one rate table: what this estate costs, written down once. Chart-declared
// values come back alongside the UI-authored overlay, each marked, so the
// screen can show what it cannot let you edit.
export function useRates() {
  return useQuery({
    queryKey: queryKeys.rates,
    queryFn: () => apiGet<RatesResponse>("/api/v1/rates"),
  });
}

// A rate change re-prices every screen that shows money, so those queries are
// dropped alongside the table — otherwise Cost and AI keep quoting the old
// price until their next poll, which reads as the edit not having worked.
function useInvalidateRates() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.rates });
    void qc.invalidateQueries({
      predicate: (q) => q.queryKey[1] === "cost" || q.queryKey[1] === "ai",
    });
  };
}

export function useSaveRates() {
  const invalidate = useInvalidateRates();
  return useMutation({
    mutationFn: (table: RateTable) =>
      apiPut<RatesResponse>("/api/v1/rates", table),
    onSuccess: invalidate,
  });
}

// Clearing the overlay returns the install to whatever the chart declares — it
// does not un-price an estate that declared rates in values.
export function useClearRates() {
  const invalidate = useInvalidateRates();
  return useMutation({
    mutationFn: () => apiDelete("/api/v1/rates"),
    onSuccess: invalidate,
  });
}
