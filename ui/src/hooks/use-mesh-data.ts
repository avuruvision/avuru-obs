"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type { MeshControlPlane, MeshProxiesResponse } from "@/lib/api-types";

// The mesh's own workloads — the ones every other screen deliberately hides,
// because their edges are hops rather than dependencies. Here they are the
// subject.
export function useMeshProxies(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.meshProxies(project, time),
    queryFn: () => apiGet<MeshProxiesResponse>("/api/v1/mesh/proxies", { ...time }, { project }),
  });
}

// Control-plane health. The response leads with `available`, and an
// unavailable one carries a reason rather than zeros — see the endpoint.
export function useMeshControlPlane(time: TimeParams) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.meshControlPlane(project, time),
    queryFn: () => apiGet<MeshControlPlane>("/api/v1/mesh/control-plane", { ...time }, { project }),
  });
}
