"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiDelete, apiGet, apiPost, apiPut } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type {
  OIDCMappingInput,
  OIDCMappingResponse,
  OIDCMappingRule,
} from "@/lib/api-types";

// The merged OIDC group→role mapping (chart config + DB overlay), exactly as
// auth.MergeMapping resolves it server-side. Instance-global, like
// serviceGroups: it drives SSO grants for every project, not one tenant's
// data. The caller gates `enabled` on SSO being configured — the hub only
// registers these routes when cfg.OIDCMapping != nil, so querying with SSO
// off would just 404.
export function useOIDCMapping(enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.oidcMapping,
    queryFn: () => apiGet<OIDCMappingResponse>("/api/v1/auth/oidc/mapping"),
    enabled,
  });
}

function useInvalidateOIDCMapping() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: queryKeys.oidcMapping });
  };
}

// PUT upserts one rule by group (the path, not the body — mirrors
// oidcMappingInput, which carries only role + projects). One mutation for
// both add and edit, like useUpdateServiceGroup. A group the chart also
// declares is a legitimate target: the hub stores it and answers Shadowed
// rather than refusing it outright.
export function useSaveOIDCMapping() {
  const invalidate = useInvalidateOIDCMapping();
  return useMutation({
    mutationFn: ({ group, ...input }: { group: string } & OIDCMappingInput) =>
      apiPut<OIDCMappingRule>(
        `/api/v1/auth/oidc/mapping/${encodeURIComponent(group)}`,
        input,
      ),
    onSuccess: invalidate,
  });
}

// DELETE tombstones an AUTHORED rule (shadowed or not — d7ef9a0). A
// chart-declared name with no authored row behind it 400s, but the panel
// never offers Delete for a config-only row in the first place, so that
// refusal is a backstop here, not something this mutation needs to predict.
export function useDeleteOIDCMapping() {
  const invalidate = useInvalidateOIDCMapping();
  return useMutation({
    mutationFn: (group: string) =>
      apiDelete(`/api/v1/auth/oidc/mapping/${encodeURIComponent(group)}`),
    onSuccess: invalidate,
  });
}

// POST reset clears every authored rule, returning the install to exactly
// what the chart declares — the explicit "start over" the panel confirms
// before firing. Answers 204, so the OIDCMappingResponse the list re-fetches
// (via invalidate) is the source of truth, not this call's own response.
export function useResetOIDCMapping() {
  const invalidate = useInvalidateOIDCMapping();
  return useMutation({
    mutationFn: () => apiPost<void>("/api/v1/auth/oidc/mapping/reset", {}),
    onSuccess: invalidate,
  });
}
