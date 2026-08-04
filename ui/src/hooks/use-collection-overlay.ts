"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPut } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { CollectionOverlay, CollectionOverlayResponse } from "@/lib/api-types";

// The runtime collection overlay + its resolved effective state. Admin-only
// on the hub (securedAdmin → 403) and instance-global — it drives the
// release-wide sensor DaemonSet, so the key carries no project element.
export function useCollectionOverlay(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.collectionOverlay,
    queryFn: () => apiGet<CollectionOverlayResponse>("/api/v1/collection/overlay"),
    enabled,
    staleTime: 30_000,
  });
}

function useInvalidateCollectionOverlay() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.collectionOverlay });
  };
}

// PUT replaces the overlay wholesale (the hub has no PATCH merge) and applies
// it to the cluster before answering. The PUT response carries no effective
// state, so the invalidated GET is what refreshes the resolved view.
export function useSaveCollectionOverlay() {
  const invalidate = useInvalidateCollectionOverlay();
  return useMutation({
    mutationFn: (overlay: CollectionOverlay) =>
      apiPut<CollectionOverlayResponse>("/api/v1/collection/overlay", overlay),
    onSuccess: invalidate,
  });
}

// DELETE is the explicit "back to chart defaults" — the hub persists the
// empty overlay (keeping the audit trail) and reconciles the cluster.
export function useResetCollectionOverlay() {
  const invalidate = useInvalidateCollectionOverlay();
  return useMutation({
    mutationFn: () => apiDelete("/api/v1/collection/overlay"),
    onSuccess: invalidate,
  });
}
