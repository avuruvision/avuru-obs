"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  CreateIngestKeyRequest,
  CreateIngestKeyResponse,
  IngestKeysResponse,
} from "@/lib/api-types";

// Ingest keys for one project. Admin-only on the hub (securedAdmin → 403);
// the card also gates on isAdmin as defense in depth. The list never carries
// the raw secret — only prefix + metadata.
export function useIngestKeys(project: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.ingestKeys(project),
    queryFn: () =>
      apiGet<IngestKeysResponse>(
        `/api/v1/projects/${encodeURIComponent(project)}/keys`,
      ),
    enabled,
    staleTime: 30_000,
  });
}

function useInvalidateIngestKeys(project: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.ingestKeys(project) });
  };
}

// Create returns the raw key exactly once (the caller shows it in a copy
// dialog); the list is invalidated so the new key's metadata appears.
export function useCreateIngestKey(project: string) {
  const invalidate = useInvalidateIngestKeys(project);
  return useMutation({
    mutationFn: (input: CreateIngestKeyRequest) =>
      apiPost<CreateIngestKeyResponse>(
        `/api/v1/projects/${encodeURIComponent(project)}/keys`,
        input,
      ),
    onSuccess: invalidate,
  });
}

export function useRevokeIngestKey(project: string) {
  const invalidate = useInvalidateIngestKeys(project);
  return useMutation({
    mutationFn: (keyHash: string) =>
      apiDelete(
        `/api/v1/projects/${encodeURIComponent(project)}/keys/${encodeURIComponent(keyHash)}`,
      ),
    onSuccess: invalidate,
  });
}
