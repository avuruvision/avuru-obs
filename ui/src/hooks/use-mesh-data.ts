"use client";

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiGet } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import { queryKeys, type TimeParams } from "@/lib/query-keys";
import type {
  MeshConfigResponse,
  MeshControlPlane,
  MeshNamespacesResponse,
  MeshProxiesResponse,
  MeshSecurityResponse,
  MeshWaypointServes,
  MeshWorkloadDetail,
  MeshWorkloadLogsResponse,
  MeshWorkloadRequests,
  MeshWorkloadsResponse,
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

// Mesh configuration objects. No time window in the key: this is current
// cluster state, and refetching it because someone moved the range would be
// asking a question whose answer cannot have changed.
export function useMeshConfig(
  enabled: boolean,
  filters: { kind?: string; namespace?: string; name?: string } = {},
) {
  const { project } = useProject();
  const { kind, namespace, name } = filters;
  return useQuery({
    enabled,
    queryKey: queryKeys.meshConfig(project, kind, namespace, name),
    queryFn: () =>
      apiGet<MeshConfigResponse>("/api/v1/mesh/config", { kind, namespace, name }, { project }),
  });
}

// What the cluster declared about mutual TLS beside what the proxies observed,
// per workload. `enabled` for the same reason as the namespaces: the read joins
// two stores and is only wanted while the Security tab is the one open.
export function useMeshSecurity(time: TimeParams, enabled: boolean) {
  const { project } = useProject();
  return useQuery({
    enabled,
    queryKey: queryKeys.meshSecurity(project, time),
    queryFn: () =>
      apiGet<MeshSecurityResponse>("/api/v1/mesh/security", { ...time }, { project }),
  });
}

// One workload's requests as its proxy counted them — by response flag,
// destination version and caller. `enabled` is false when the proxy has no
// namespace: the route is keyed by namespace and name, and guessing one would
// ask the hub about a workload that does not exist.
export function useMeshWorkloadRequests(
  time: TimeParams,
  namespace: string,
  name: string,
  enabled: boolean,
) {
  const { project } = useProject();
  return useQuery({
    enabled,
    queryKey: queryKeys.meshWorkloadRequests(project, time, namespace, name),
    queryFn: () =>
      apiGet<MeshWorkloadRequests>(
        `/api/v1/mesh/workloads/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/requests`,
        { ...time },
        { project },
      ),
  });
}

// Workloads as the CLUSTER runs them, joined to what telemetry saw. Gated like
// the namespaces read, and for the same reason. The mode filter goes to the
// hub, which owns what "declared-only" means; the time window is only for the
// traffic columns.
export function useMeshWorkloads(
  time: TimeParams,
  enabled: boolean,
  filters: { namespace?: string; mode?: string } = {},
) {
  const { project } = useProject();
  const { namespace, mode } = filters;
  return useQuery({
    enabled,
    queryKey: queryKeys.meshWorkloads(project, time, namespace, mode),
    queryFn: () =>
      apiGet<MeshWorkloadsResponse>(
        "/api/v1/mesh/workloads",
        { ...time, namespace, mode },
        { project },
      ),
  });
}

// One workload whole: its row, its own findings, the policies that cover it
// with THEIR findings, and its pods.
export function useMeshWorkload(
  time: TimeParams,
  enabled: boolean,
  namespace: string,
  name: string,
) {
  const { project } = useProject();
  return useQuery({
    enabled: enabled && !!namespace && !!name,
    queryKey: queryKeys.meshWorkload(project, time, namespace, name),
    queryFn: () =>
      apiGet<MeshWorkloadDetail>(
        `/api/v1/mesh/workloads/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
        { ...time },
        { project },
      ),
  });
}

// What a waypoint serves. No time in the key: bindings are cluster state, and
// a moved range cannot change them.
export function useMeshWaypointServes(enabled: boolean, namespace: string, name: string) {
  const { project } = useProject();
  return useQuery({
    enabled: enabled && !!namespace && !!name,
    queryKey: queryKeys.meshWaypoint(project, namespace, name),
    queryFn: () =>
      apiGet<MeshWaypointServes>(
        `/api/v1/mesh/waypoints/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
        undefined,
        { project },
      ),
  });
}

export interface WorkloadLogFilters {
  q?: string;
  severity?: string;
  // Comma list of app, ztunnel, waypoint; absent means all three.
  source?: string;
  // "namespace/name" of the waypoint to read when the hub cannot resolve
  // the binding itself.
  waypoint?: string;
}

// A workload's logs from its three sources as one stream — the same infinite
// page shape as the logs screen's, so the same table renders it.
export function useMeshWorkloadLogs(
  time: TimeParams,
  enabled: boolean,
  namespace: string,
  name: string,
  filters: WorkloadLogFilters,
) {
  const { project } = useProject();
  return useInfiniteQuery({
    enabled: enabled && !!namespace && !!name,
    queryKey: queryKeys.meshWorkloadLogs(project, time, namespace, name, { ...filters }),
    queryFn: ({ pageParam }) =>
      apiGet<MeshWorkloadLogsResponse>(
        `/api/v1/mesh/workloads/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/logs`,
        { ...time, ...filters, limit: 100, cursor: pageParam || undefined },
        { project },
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  });
}
