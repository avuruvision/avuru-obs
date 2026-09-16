import type { NodeStats, PodConnection, PodRef, PodStats } from "@/lib/api-types";

export const MAX_NODES = 9;
export const PODS_PER_NODE = 12;
export const MAX_FLOWS = 100;
export const podKey = (p: Pick<PodStats, "node" | "namespace" | "name">) => JSON.stringify([p.node, p.namespace, p.name]);
export const podMatches = (p: PodStats, ref: PodRef) => p.name === ref.name && p.namespace === ref.namespace && (!ref.node || p.node === ref.node);
export type Position = [number, number, number];
export interface PlacedPod { id: string; pod: PodStats; position: Position; }
export interface PlacedNode { name: string; stats?: NodeStats; position: Position; shown: number; total: number; }
export interface PlacedFlow { source: string; target?: string; edge: PodConnection; }
export interface XRayModel { nodes: PlacedNode[]; pods: PlacedPod[]; flows: PlacedFlow[]; nodeTotal: number; podTotal: number; flowTotal: number; }
export interface SceneSettings { pods: boolean; nodes: boolean; infra: boolean; spread: number; opacity: number; paused: boolean; isolated: boolean; selected?: string; }

// Stable identity order: metric refreshes and selection never move the objects.
// Hard bounds protect the GPU; the inventory and explicit filter counts explain
// what the scene omits. A missing node name is a logical unplaced group.
export function buildModel(nodes: NodeStats[], pods: PodStats[], connections: PodConnection[]): XRayModel {
  const names = [...new Set([...nodes.map(n => n.name), ...pods.map(p => p.node)])].sort();
  const visible = names.slice(0, MAX_NODES);
  const columns = Math.ceil(Math.sqrt(visible.length));
  const placedPods: PlacedPod[] = [];
  const placedNodes = visible.map((name, index) => {
    const position: Position = [(index % columns - (columns - 1) / 2) * 4.4, 0, (Math.floor(index / columns) - (Math.ceil(visible.length / columns) - 1) / 2) * 4.5];
    const members = pods.filter(p => p.node === name).sort((a, b) => podKey(a).localeCompare(podKey(b)));
    members.slice(0, PODS_PER_NODE).forEach((pod, slot) => placedPods.push({
      id: podKey(pod), pod,
      position: [position[0] + (slot % 3 - 1) * 1.05, 0, position[2] + (Math.floor(slot / 3) - 1.5) * .86],
    }));
    return { name, stats: nodes.find(n => n.name === name), position, shown: Math.min(members.length, PODS_PER_NODE), total: members.length };
  });
  const flows: PlacedFlow[] = [];
  for (const edge of connections) {
    const source = placedPods.filter(p => podMatches(p.pod, edge.source));
    const target = placedPods.filter(p => podMatches(p.pod, edge.target));
    // Do not route an omitted/filtered known Pod into the unresolved-peer globe.
    if (source.length === 1 && (target.length === 1 || (!edge.target.name && edge.peer))) {
      flows.push({ source: source[0].id, target: target[0]?.id, edge });
    }
  }
  return { nodes: placedNodes, pods: placedPods, flows: flows.slice(0, MAX_FLOWS), nodeTotal: names.length, podTotal: pods.length, flowTotal: flows.length };
}
