import type { ElementDefinition } from "cytoscape";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { formatBytes, formatMs } from "@/lib/format";

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

// edgeTooltip is the hover text for an edge: call volume, this path's latency,
// plus any network health OBI measured for the connection.
function edgeTooltip(e: ServiceEdge, windowMinutes: number): string {
  const parts = [`${e.source} → ${e.target}`, formatRpm(e.calls / windowMinutes)];
  if (e.p95Ms !== undefined) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% errors`);
  if (e.rttMs) parts.push(`RTT p95 ${e.rttMs.toFixed(0)}ms`);
  if (e.failedConnections) parts.push(`${e.failedConnections} failed conns`);
  if (e.bytes) parts.push(formatBytes(e.bytes));
  return parts.join(" · ");
}

// The label an edge reveals while its neighbourhood is focused. Kept short —
// it is drawn along the line. p95 is omitted when the edge is flow-derived and
// has no span to measure; showing 0ms there would be a lie.
function edgeFocusLabel(e: ServiceEdge, windowMinutes: number): string {
  const parts = [formatRpm(e.calls / windowMinutes)];
  if (e.p95Ms !== undefined) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% err`);
  if (e.rttMs) parts.push(`RTT ${e.rttMs.toFixed(0)}ms`);
  return parts.join(" · ");
}

export interface BuildOptions {
  services: ServiceStats[];
  edges: ServiceEdge[];
  health: Map<string, ServiceHealth>;
  windowMs: number;
  carbon: boolean;
}

// Builds the cytoscape element list. Every derived string lives here so the
// React shell stays a lifecycle wrapper and the stylesheet stays declarative.
export function buildElements({
  services,
  edges,
  health,
  windowMs,
  carbon,
}: BuildOptions): ElementDefinition[] {
  const windowMinutes = Math.max(windowMs / 60_000, 1 / 60);
  const names = new Set(services.map((s) => s.name));
  // Heaviest node in view anchors the relative carbon scale (green only).
  const maxGco2e = carbon ? Math.max(0, ...services.map((s) => s.gco2e ?? 0)) : 0;

  const nodes: ElementDefinition[] = services.map((s) => {
    const rpm = s.ratePerSec * 60;
    return {
      data: {
        id: s.name,
        label: s.name,
        // The ring's channel. Absent from the rollup → "unknown", which is the
        // neutral ring: an unmeasured service is never drawn as healthy.
        status: health.get(s.name)?.status ?? "unknown",
        focusLabel: `${s.name}\n${formatRpm(rpm)} · p95 ${formatMs(s.p95Ms)}`,
        rate: s.ratePerSec,
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
        focusLabel: edgeFocusLabel(e, windowMinutes),
        tooltip: edgeTooltip(e, windowMinutes),
      },
    }));

  return [...nodes, ...links];
}
