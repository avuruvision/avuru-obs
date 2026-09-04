"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { OAuthGrantsResponse } from "@/lib/api-types";

// The applications the caller has connected. Routes are `authenticated`, not
// admin — for the same reason API tokens are: a consent is yours to withdraw,
// and it must not need someone else to withdraw it.
export function useOAuthGrants(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.oauthGrants,
    queryFn: () => apiGet<OAuthGrantsResponse>("/api/v1/auth/oauth/grants"),
    enabled,
    staleTime: 30_000,
  });
}

// Disconnecting revokes the consent AND every token issued under it, so the
// application stops reading on its next request rather than when something
// expires.
export function useRevokeOAuthGrant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiDelete(`/api/v1/auth/oauth/grants/${encodeURIComponent(id)}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.oauthGrants });
    },
  });
}
