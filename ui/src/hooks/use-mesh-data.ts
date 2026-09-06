"use client";

import { useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  MeshControlPlane,
  MeshNamespacesResponse,
  MeshProxiesResponse,
} from "@/lib/api-types";

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

// Namespaces as the CLUSTER defines them, joined to what telemetry saw.
//
// `enabled` because this is the one mesh read behind a second module: an
// install without mesh-config 404s the route, and firing the request anyway
// would put a failed query behind a tab nobody can open.
export function useMeshNamespaces(time: TimeParams, enabled: boolean) {
  const { project } = useProject();
  return useQuery({
    enabled,
    queryKey: queryKeys.meshNamespaces(project, time),
    queryFn: () =>
      apiGet<MeshNamespacesResponse>("/api/v1/mesh/namespaces", { ...time }, { project }),
  });
}
