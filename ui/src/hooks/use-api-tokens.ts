"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  ApiTokensResponse,
  CreateApiTokenRequest,
  CreateApiTokenResponse,
} from "@/lib/api-types";

// The caller's own API tokens — metadata only, the raw secret appears exactly
// once in the create response. Routes are `authenticated`, not admin: a user
// whose grants were all revoked can still clean up the credentials they
// handed out, exactly as they can still log out.
export function useApiTokens(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.apiTokens,
    queryFn: () => apiGet<ApiTokensResponse>("/api/v1/tokens"),
    enabled,
    staleTime: 30_000,
  });
}

function useInvalidateApiTokens() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.apiTokens });
  };
}

// Create returns the raw token exactly once (the caller shows it in a copy
// dialog); the list is invalidated so the new token's metadata appears.
export function useCreateApiToken() {
  const invalidate = useInvalidateApiTokens();
  return useMutation({
    mutationFn: (input: CreateApiTokenRequest) =>
      apiPost<CreateApiTokenResponse>("/api/v1/tokens", input),
    onSuccess: invalidate,
  });
}

export function useRevokeApiToken() {
  const invalidate = useInvalidateApiTokens();
  return useMutation({
    mutationFn: (tokenHash: string) =>
      apiDelete(`/api/v1/tokens/${encodeURIComponent(tokenHash)}`),
    onSuccess: invalidate,
  });
}
