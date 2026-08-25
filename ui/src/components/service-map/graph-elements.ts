import type { ElementDefinition } from "cytoscape";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { formatBytes, formatMs } from "@/lib/format";
import { ROLE_PEER } from "@/lib/map-peers";

// carbonBucket maps a node's gCO2e into the 3-step halo scale, relative to the
// heaviest node in view so the lens is meaningful at any absolute scale.
function carbonBucket(gco2e: number, max: number): 0 | 1 | 2 {
  if (max <= 0) return 0;
  const r = gco2e / max;
  return r < 1 / 3 ? 0 : r < 2 / 3 ? 1 : 2;
}

// An edge is network-unhealthy when RTT p95 is high or any connections failed.
const RTT_UNHEALTHY_MS = 100;
function edgeUnhealthy(e: ServiceEdge): boolean {
  return (e.rttMs ?? 0) > RTT_UNHEALTHY_MS || (e.failedConnections ?? 0) > 0;
}

function formatRpm(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k rpm`;
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} rpm`;
}

// nodeEnergyTooltip is the hover text a node gains under the carbon overlay.
export function nodeEnergyTooltip(label: string, wh: number, gco2e: number): string {
  return `${label} · ${wh.toFixed(1)} Wh · ${gco2e.toFixed(2)} gCO2e`;
}

// A flow-derived edge is a connection the kernel saw, not a call anyone traced:
// it carries bytes and zero calls by construction. Saying "0 rpm" on it reads as
// "this path is idle" when the truth is "nobody measured this path".
function isFlowOnly(e: ServiceEdge): boolean {
  return e.calls === 0;
}

// The proxies a dependency was recovered across, for the hover. Named rather
// than counted: "via istio-proxy" tells the reader why they cannot see the hop,
// where "1 hop" would just raise the question.
function viaLabel(e: ServiceEdge): string | undefined {
  const via = e.viaTransport;
  if (!via?.length) return undefined;
  return `via ${via.join(", ")}`;
}

// edgeTooltip is the hover text for an edge: call volume, this path's latency,
// plus any network health OBI measured for the connection.
function edgeTooltip(e: ServiceEdge, windowMinutes: number): string {
  const parts = [`${e.source} → ${e.target}`];
  parts.push(isFlowOnly(e) ? "network flow · no traced calls" : formatRpm(e.calls / windowMinutes));
  if (e.p95Ms !== undefined) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% errors`);
  if (e.rttMs) parts.push(`RTT p95 ${e.rttMs.toFixed(0)}ms`);
  if (e.failedConnections) parts.push(`${e.failedConnections} failed conns`);
  if (e.bytes) parts.push(formatBytes(e.bytes));
  const via = viaLabel(e);
  if (via) parts.push(via);
  return parts.join(" · ");
}

// The label an edge reveals while its neighbourhood is focused. Kept short —
// it is drawn along the line. p95 is omitted when the edge is flow-derived and
// has no span to measure; showing 0ms there would be a lie, and so would 0 rpm.
function edgeFocusLabel(e: ServiceEdge, windowMinutes: number): string {
  const parts = [isFlowOnly(e) ? "network flow" : formatRpm(e.calls / windowMinutes)];
  if (e.p95Ms !== undefined) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% err`);
  if (e.rttMs) parts.push(`RTT ${e.rttMs.toFixed(0)}ms`);
  const via = viaLabel(e);
  if (via) parts.push(via);
  return parts.join(" · ");
}

// The persistent edge label: volume only. The hover already answers "what is
// this one edge", so the always-on label exists for the other question — which
// of these paths carries the traffic — and anything beyond the number makes a
// dense graph unreadable. A flow-derived edge shows bytes, because it has no
// calls to count.
function edgeVolumeLabel(e: ServiceEdge, windowMinutes: number): string {
  if (isFlowOnly(e)) return e.bytes ? formatBytes(e.bytes) : "";
  return formatRpm(e.calls / windowMinutes);
}

// A virtual target is named as a URI (`postgresql://orders-db`) so its node id
// can never collide with a service.name. That is the right identity and the
// wrong LABEL — the scheme repeats what the shape and the legend already say,
// twice per node, on the busiest part of the graph. So the label drops it and
// the hover restores it in full.
export function virtualLabel(name: string): string {
  const at = name.indexOf("://");
  return at < 0 ? name : name.slice(at + 3);
}

// The hover text for a derived dependency: what it is, how hard it is being
// used, and what the callers are getting back. Deliberately says "callers" —
// everything here is measured from the calling side, because the target itself
// tells us nothing.
export function virtualTooltip(s: ServiceStats, rpm: number): string {
  const parts = [s.name, s.kind ?? "dependency", formatRpm(rpm)];
  if (s.p95Ms) parts.push(`p95 ${formatMs(s.p95Ms)} at the caller`);
  if (s.errorRate > 0) parts.push(`${(s.errorRate * 100).toFixed(1)}% failed`);
  return parts.join(" · ");
}

// How the map draws boundaries. "namespace" is where a workload lives;
// "group" is what it belongs to on the health board. Two different questions,
// so the user picks — and "none" stays the default, because a box around every
// node is only clarifying once you asked for it.
export type MapGrouping = "none" | "namespace" | "group";

// Boundary ids are namespaced with a control character so a boundary can never
// collide with a service.name — a collision would make a box and a service the
// same graph node. Nothing renders the id; the label is a separate field.
const BOUNDARY_PREFIX = "\u0000boundary\u0000";

export function boundaryId(label: string): string {
  return BOUNDARY_PREFIX + label;
}

export function isBoundaryId(id: string): boolean {
  return id.startsWith(BOUNDARY_PREFIX);
}

export interface BuildOptions {
  services: ServiceStats[];
  edges: ServiceEdge[];
  health: Map<string, ServiceHealth>;
  windowMs: number;
  carbon: boolean;
  healthEnabled: boolean;
  grouping?: MapGrouping;
}

// Builds the cytoscape element list. Every derived string lives here so the
// React shell stays a lifecycle wrapper and the stylesheet stays declarative.
export function buildElements({
  services,
  edges,
  health,
  windowMs,
  carbon,
  healthEnabled,
  grouping = "none",
}: BuildOptions): ElementDefinition[] {
  const windowMinutes = Math.max(windowMs / 60_000, 1 / 60);
  const names = new Set(services.map((s) => s.name));
  // Heaviest node in view anchors the relative carbon scale (green only).
  const maxGco2e = carbon ? Math.max(0, ...services.map((s) => s.gco2e ?? 0)) : 0;

  // Which box a node belongs in, or nothing. A service that declares no
  // namespace, and one the health rollup has not placed in a group, are drawn
  // OUTSIDE every boundary — the honest answer, rather than sweeping them into
  // an invented "other" that reads like a real place.
  const boundaryOf = (s: ServiceStats): string | undefined => {
    if (s.role === "virtual") return undefined; // derived; it lives nowhere
    if (grouping === "namespace") return s.namespace || undefined;
    if (grouping === "group") return health.get(s.name)?.group || undefined;
    return undefined;
  };
  const boundaries = new Set<string>();
  for (const s of services) {
    const b = boundaryOf(s);
    if (b) boundaries.add(b);
  }
  const parents: ElementDefinition[] = [...boundaries].sort().map((label) => ({
    data: {
      id: boundaryId(label),
      label,
      // The base node style sizes on `rate`; a compound parent is sized by its
      // children instead, but the mapping still has to find the field.
      rate: 0,
    },
  }));

  const nodes: ElementDefinition[] = services.map((s) => {
    const rpm = s.ratePerSec * 60;
    const virtual = s.role === "virtual";
    // A peer the renderer could not resolve to a service. It carries no
    // metrics, so nothing about it is claimed — not even a zero.
    const peer = s.role === ROLE_PEER;
    return {
      data: {
        id: s.name,
        label: virtual ? virtualLabel(s.name) : s.name,
        // The ring's channel. Absent from the rollup → "unknown", which is the
        // neutral ring: an unmeasured service is never drawn as healthy.
        status: health.get(s.name)?.status ?? "unknown",
        // Module-off fallback: with no health rollup there is no status to
        // ring, so the map keeps the pre-restyle signal — a node that saw ANY
        // error in the window rings red. Deliberately its own field rather
        // than faking a "down" status: "this service erred" is a weaker claim
        // than the hub's verdict.
        ...(!healthEnabled && s.errorRate > 0 ? { errorRing: 1 } : {}),
        focusLabel: peer
          ? `${s.name}\nno telemetry of its own`
          : `${virtual ? virtualLabel(s.name) : s.name}\n${formatRpm(rpm)} · p95 ${formatMs(s.p95Ms)}`,
        rate: s.ratePerSec,
        // A derived dependency: a database, cache or broker that sends no
        // telemetry of its own. Its own field rather than a status, because
        // the ring is health's channel and we have no health verdict for
        // something we only see through its callers.
        ...(virtual ? { virtual: 1, kind: s.kind ?? "", tooltip: virtualTooltip(s, rpm) } : {}),
        ...(peer
          ? {
              peer: 1,
              tooltip: `${s.name} · seen in traffic, never heard from`,
            }
          : {}),
        // Compound membership. Absent when the map is ungrouped or the node
        // has nothing to belong to, which cytoscape reads as "no parent".
        ...(boundaryOf(s) ? { parent: boundaryId(boundaryOf(s) as string) } : {}),
        // Only mesh/gateway nodes carry `transport`, and only when the user
        // asked to see them — an application node's data is unchanged.
        ...(s.role === "transport" ? { transport: 1 } : {}),
        // Carbon fields are added ONLY under the overlay, so a non-green node
        // carries the exact same data as before.
        ...(carbon && s.gco2e !== undefined
          ? { wh: s.wh, gco2e: s.gco2e, carbon: carbonBucket(s.gco2e, maxGco2e) }
          : {}),
      },
    };
  });

  // Only edges between known nodes (a callee may have aged out of the window).
  const links: ElementDefinition[] = edges
    .filter((e) => names.has(e.source) && names.has(e.target) && e.source !== e.target)
    .map((e) => ({
      data: {
        id: `${e.source}->${e.target}`,
        source: e.source,
        target: e.target,
        calls: e.calls,
        error: e.errorRate,
        health: edgeUnhealthy(e) ? 1 : 0,
        // Its own channel so the stylesheet can draw "connection we observed"
        // differently from "call we traced" without borrowing dashed, which
        // already means network-unhealthy.
        flow: isFlowOnly(e) ? 1 : 0,
        focusLabel: edgeFocusLabel(e, windowMinutes),
        volumeLabel: edgeVolumeLabel(e, windowMinutes),
        tooltip: edgeTooltip(e, windowMinutes),
      },
    }));

  // Parents first: cytoscape resolves `parent` references within one batch,
  // and listing the boxes ahead of their members keeps that obvious.
  return [...parents, ...nodes, ...links];
}
