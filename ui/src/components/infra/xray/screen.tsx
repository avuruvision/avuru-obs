"use client";

import { useMemo, useState, useSyncExternalStore } from "react";
import { usePodConnections } from "@/hooks/use-infra-data";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useProject } from "@/lib/project-context";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import type { NodeStats, PodStats } from "@/lib/api-types";
import { buildModel, MAX_NODES, PODS_PER_NODE, podKey, type SceneSettings } from "./model";
import { XRayCanvas } from "./canvas";
import { XRayControls } from "./controls";
import { XRayInspector } from "./inspector";

const subscribeMotion = (callback: () => void) => {
  const media = matchMedia("(prefers-reduced-motion: reduce)"); media.addEventListener("change", callback);
  return () => media.removeEventListener("change", callback);
};
const readMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;
const noMotion = () => true;

export default function ClusterXRay({ nodes, pods, allNodes, loading, podsError }: {
  nodes: NodeStats[]; pods: PodStats[]; allNodes: NodeStats[]; loading: boolean; podsError: boolean;
}) {
  const { time } = useTimeRange(), { project } = useProject(), { get, setMany } = useURLState();
  const node = get("node"), selected = get("pod");
  const connections = usePodConnections(time, node);
  const reducedMotion = useSyncExternalStore(subscribeMotion, readMotion, noMotion);
  const [controls, setControls] = useState<Partial<SceneSettings>>({});
  const sceneNodes = useMemo(() => node ? nodes.filter(n => n.name === node) : nodes, [node, nodes]);
  const model = useMemo(() => buildModel(sceneNodes, pods, connections.isError ? [] : connections.data?.connections ?? []), [sceneNodes, pods, connections.data, connections.isError]);
  const pod = pods.find(p => podKey(p) === selected);
  const visible = model.pods.some(p => p.id === selected);
  const settings: SceneSettings = { pods: true, nodes: true, infra: true, spread: 68, opacity: 72, ...controls, selected,
    paused: reducedMotion || (controls.paused ?? false), isolated: get("isolate") === "true" && visible };
  const bounded = model.pods.length < model.podTotal || model.nodes.length < model.nodeTotal;
  const cpuMeasured = sceneNodes.some(n => n.cpuSeries.length > 0 || n.cpuUsageCores > 0);
  const memoryMeasured = sceneNodes.some(n => n.memorySeries.length > 0 || n.memoryUsageBytes > 0);
  const cpu = sceneNodes.reduce((sum, n) => sum + n.cpuUsageCores, 0);
  const memory = sceneNodes.reduce((sum, n) => sum + n.memoryUsageBytes, 0);
  const network = sceneNodes.reduce((sum, n) => sum + n.networkRxBytesPerSec + n.networkTxBytesPerSec, 0);
  const inventory = () => setMany({ view: undefined, isolate: undefined });
  return <div data-testid="cluster-xray" className="space-y-4">
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      {[["OBSERVED NODES", String(sceneNodes.length)], ["OBSERVED NODE CPU", cpuMeasured ? `${cpu.toFixed(2)} cores` : "Not reported"], ["OBSERVED NODE MEMORY", memoryMeasured ? formatBytes(memory) : "Not reported"], ["REPORTED NODE RX + TX", network > 0 ? `${formatBytes(network)}/s` : "Idle / unreported"]].map(([label, value]) => <div key={label} className="rounded-lg border border-neutral bg-base-200 px-4 py-3"><p className="text-[9px] tracking-widest text-base-content/65">{label}</p><p className="mt-2 text-xl font-medium tabular-nums">{value}</p></div>)}
    </div>
    <div className="flex flex-wrap items-center justify-between gap-3">
      <Select ariaLabel="Focus node in X-Ray" className="w-64 max-w-full" value={node ?? "__all__"} onChange={v => setMany({ node: v === "__all__" ? undefined : v, isolate: undefined })}
        options={[{ value: "__all__", label: "All nodes" }, ...allNodes.map(n => ({ value: n.name, label: n.name }))]} />
      <Select ariaLabel="Select Pod to inspect" className="w-80 max-w-full" value={selected ?? "__none__"} onChange={v => setMany({ pod: v === "__none__" ? undefined : v, isolate: undefined })}
        options={[{ value: "__none__", label: selected && !pod ? "Selection outside this view" : "Select a Pod to inspect" }, ...pods.map(p => ({ value: podKey(p), label: p.name, hint: `${p.namespace} · ${p.node || "Node not reported"}` }))]} />
    </div>
    {loading && <p role="status" className="text-sm text-base-content/65">Loading Pod metrics…</p>}
    {podsError && <p role="alert" className="text-sm text-error">Pod metrics could not be loaded. Node placement is still available.</p>}
    {!loading && !podsError && !pods.length && <p className="text-sm text-base-content/65">No Pods in the current window and filters.</p>}
    {bounded && <p role="status" className="text-xs text-warning">Scene shows {model.nodes.length} of {model.nodeTotal} nodes and {model.pods.length} of {model.podTotal} loaded Pods. Limit: {MAX_NODES} nodes, {PODS_PER_NODE} Pods per node. Narrow your filters to inspect the rest.</p>}
    <div className="xray-surface overflow-hidden rounded-xl border border-neutral">
      <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="min-w-0">
          <XRayCanvas key={project} model={model} settings={settings} onSelect={id => setMany({ pod: id })} onInventory={inventory} />
          <XRayControls settings={settings} reducedMotion={reducedMotion} onChange={patch => setControls(current => ({ ...current, ...patch }))} />
          <div className="flex flex-wrap gap-x-5 gap-y-2 border-t border-neutral px-5 py-3 text-[10px] text-base-content/75"><span>↗ Caller-observed requests</span><span className="text-error">↗ Requests with errors</span><span>Globe = unresolved peers, location unknown</span></div>
        </div>
        <XRayInspector pod={pod} selected={selected} edges={connections.data?.connections ?? []} connectionsError={connections.isError} visible={visible} isolated={settings.isolated}
          onClear={() => setMany({ pod: undefined, isolate: undefined })} onIsolate={() => setMany({ isolate: settings.isolated ? undefined : "true" })} />
      </div>
    </div>
    <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-base-content/65">
      <p>Logical infrastructure illustration · up to 200 Pods loaded per node filter · particle speed is illustrative.</p>
      {connections.isError ? <div className="flex items-center gap-2"><span className="text-warning">Connections unavailable.</span><Button size="sm" onClick={() => connections.refetch()}>Retry connections</Button></div>
        : connections.isLoading ? <span role="status">Loading connections…</span>
          : <span>{model.flows.length} connections in view{connections.data?.truncated || model.flows.length < model.flowTotal ? " · connection limit reached" : ""}</span>}
      {reducedMotion && <span>Flow animation paused by your reduced-motion preference.</span>}
    </div>
  </div>;
}
