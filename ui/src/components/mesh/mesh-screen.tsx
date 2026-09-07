"use client";

import { useMemo } from "react";
import { Waypoints } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshProxies, useMeshControlPlane } from "@/hooks/use-mesh-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { Tabs } from "@/components/ui/tabs";
import { ControlPlaneCard } from "./control-plane-card";
import { ProxiesPanel } from "./proxies-panel";
import { ProxyDetail } from "./proxy-detail";
import { MeshGraph } from "./mesh-graph";
import { NamespacesTable } from "./namespaces-table";
import { ConfigBrowser } from "./config-browser";
import { SecurityTab } from "./security-tab";
import { WorkloadsTable } from "./workloads-table";
import { WorkloadDetail } from "./workload-detail";
import { useMeshNamespaces, useMeshSecurity } from "@/hooks/use-mesh-data";
import { useCapabilities } from "@/hooks/use-capabilities";
import { useServiceMapData } from "@/hooks/use-service-map-data";

// The mesh's own screen.
//
// Every other surface hides these workloads on purpose — their edges are hops,
// not dependencies, so a dependency graph that draws them is lying. That is the
// right call for the map and the wrong final word: on a cluster where the mesh
// IS the network, a proxy dropping requests or a control plane that has stopped
// pushing config is the outage.
type MeshView = "proxies" | "graph" | "security" | "namespaces" | "config" | "workloads";
// Security is a base view: the observed half comes from the proxies' own
// scrape, which the mesh module runs. Without the config module the tab still
// says what was seen — and says, in so many words, that nothing was declared.
const BASE_VIEWS: { value: MeshView; label: string }[] = [
  { value: "proxies", label: "Proxies" },
  { value: "graph", label: "Graph" },
  { value: "security", label: "Security" },
];
// Namespaces, configuration and workloads come from the cluster, not from
// traffic, so these tabs appear only where the module that reads the cluster
// is on.
const CONFIG_VIEWS: { value: MeshView; label: string }[] = [
  { value: "namespaces", label: "Namespaces" },
  { value: "config", label: "Configuration" },
  { value: "workloads", label: "Workloads" },
];

export function MeshScreen() {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();
  const selected = get("proxy") ?? "";
  const workload = get("wl") ?? "";
  const { data: caps } = useCapabilities();
  const configOn = caps?.modules.includes("mesh-config") ?? false;
  const requested = get("view");
  const view: MeshView =
    requested === "graph" || requested === "security"
      ? requested
      : configOn && CONFIG_VIEWS.some((v) => v.value === requested)
        ? (requested as MeshView)
        : "proxies";
  const views = configOn ? [...BASE_VIEWS, ...CONFIG_VIEWS] : BASE_VIEWS;

  const proxies = useMeshProxies(time);
  const controlPlane = useMeshControlPlane(time);
  // Only fetched for the graph: the proxy table needs none of it, and the map
  // read is the most expensive one on the screen.
  const map = useServiceMapData(time);
  const nsConfig = useMeshNamespaces(time, configOn);
  // Fetched only while its tab is open: the read joins two stores, and no
  // other view on the screen needs it.
  const security = useMeshSecurity(time, view === "security");

  const list = useMemo(() => proxies.data?.proxies ?? [], [proxies.data]);

  if (proxies.isLoading) return <CenteredSpinner />;

  if (selected) {
    const proxy = list.find((p) => p.name === selected);
    // A name with no proxy behind it in this window: a stale link, or a proxy
    // that stopped reporting. Say which rather than rendering an empty page.
    if (!proxy) {
      return (
        <EmptyState icon={Waypoints} title={`No proxy named ${selected} in this window`}>
          It may have been removed, renamed, or simply sent no telemetry in the
          selected range. Widen the time range, or go back to the full list.
        </EmptyState>
      );
    }
    return <ProxyDetail proxy={proxy} onBack={() => setMany({ proxy: undefined })} />;
  }

  // A workload is "namespace/name"; the name may itself carry no slash, so the
  // first one is the split.
  if (view === "workloads" && workload) {
    const slash = workload.indexOf("/");
    return (
      <WorkloadDetail
        namespace={slash < 0 ? "" : workload.slice(0, slash)}
        name={slash < 0 ? workload : workload.slice(slash + 1)}
        onBack={() => setMany({ wl: undefined })}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <ControlPlaneCard data={controlPlane.data} loading={controlPlane.isLoading} />
      <Tabs items={views} value={view} onChange={(v) => setMany({ view: v === "proxies" ? undefined : v })} />
      {view === "graph" ? (
        <MeshGraph
          services={map.data?.services ?? []}
          edges={map.data?.edges ?? []}
          windowMs={windowMs}
          proxies={list}
        />
      ) : view === "security" ? (
        <SecurityTab data={security.data} loading={security.isLoading} />
      ) : view === "config" ? (
        <ConfigBrowser />
      ) : view === "workloads" ? (
        <WorkloadsTable />
      ) : view === "namespaces" ? (
        nsConfig.isLoading ? (
          <CenteredSpinner />
        ) : nsConfig.data ? (
          <NamespacesTable data={nsConfig.data} />
        ) : null
      ) : (
        <ProxiesPanel proxies={list} />
      )}
    </div>
  );
}
