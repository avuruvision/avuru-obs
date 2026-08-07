"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { CapabilitiesResponse, ModuleName } from "@/lib/api-types";

// Which signal families this install runs. Instance-global (never project
// scoped): modules are an install-level choice. The value only changes on a
// redeploy, so cache it hard.
export function useCapabilities() {
  return useQuery({
    queryKey: queryKeys.capabilities,
    queryFn: () => apiGet<CapabilitiesResponse>("/api/v1/capabilities"),
    staleTime: 5 * 60_000,
  });
}

// Is `module` active? Returns true while the answer is UNKNOWN — loading, or
// a hub predating /api/v1/capabilities. Modules are all-on by default, so
// "show it" is the safe guess: a wrong guess costs one 404 the page already
// renders an error for, while guessing "hide" would blank the nav on every
// load and break older hubs outright.
export function useModuleEnabled(module: ModuleName): boolean {
  const { data } = useCapabilities();
  return data ? data.modules.includes(module) : true;
}

// Whether the runtime collection control plane is live (chart flag
// collection.runtimeControl.enabled). The default while UNKNOWN is the
// opposite of useModuleEnabled's: writable switches must not appear unless
// the hub says the overlay routes exist — guessing "show" would render
// controls whose every save 404s, while guessing "hide" just leaves the
// read-only page every install had before the flag.
export function useCollectionRuntimeControl(): boolean {
  const { data } = useCapabilities();
  return data?.collectionRuntimeControl ?? false;
}
