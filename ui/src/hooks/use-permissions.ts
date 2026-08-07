"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { PermissionsResponse } from "@/lib/api-types";

// What each role may do, as the hub reports it. Deliberately fetched rather
// than hard-coded: the hub derives it from the guards its routes registered
// with, so a matrix rendered from this cannot drift from what is enforced.
// Instance-global and changes only on a redeploy, so cache it hard.
export function usePermissions() {
  return useQuery({
    queryKey: queryKeys.permissions,
    queryFn: () => apiGet<PermissionsResponse>("/api/v1/auth/permissions"),
    staleTime: 5 * 60_000,
  });
}
