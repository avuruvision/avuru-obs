"use client";

import { useMemo } from "react";
import Link from "next/link";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/format";
import { useTimeRange } from "@/hooks/use-time-range";
import { useNodesData, usePodsData, useZoneTraffic } from "@/hooks/use-infra-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import type { NodeStats, PodStats, ZoneTraffic } from "@/lib/api-types";

// Band 3 of the Dashboard: Kubernetes capacity, as far as the data honestly
// goes.
//
// What is NOT here, deliberately: a CPU utilization percentage. NodeStats
// carries cpuUsageCores with no capacity or allocatable field anywhere in the
// hub or the store — kubeletstats alone cannot supply it (that needs the
// k8s_cluster receiver). A percentage would have to invent its denominator, so
// CPU is reported as cores in use, an absolute the sensor actually measures.
// Memory DOES have both halves (usage + available), so its bar is real. Cluster
// storage is likewise absent from the sensor; the Storage tab's disk figures are
// ClickHouse's own volumes, which is a different question.

function formatCores(cores: number): string {
  return cores >= 10 ? cores.toFixed(1) : cores.toFixed(2);
}

// Utilization thresholds. The bar carries a status color AND its number, never
// color alone — the figure is the accessible encoding, the hue is the glance.
const WARN_AT = 0.8;
const CRIT_AT = 0.9;

function barTone(ratio: number): string {
  if (ratio >= CRIT_AT) return "bg-error";
  if (ratio >= WARN_AT) return "bg-warning";
  return "bg-primary";
}

// Nodes shown as bars; the rest is a link. A dashboard band is a glance, and
// the Nodes screen (sortable and filterable since W5) is where a full roster
// belongs.
const MAX_BARS = 6;

// Zone crossings shown before the list is truncated. Cardinality here is zone
// pairs, so a realistic cluster has a handful; the cap exists for the estate
// that spans many regions, not for the common case.
const MAX_ZONE_PAIRS = 6;

function StatTile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card className="flex flex-col gap-0.5 p-3">
      <span className="text-xs text-base-content/45">{label}</span>
      <span className="text-xl font-semibold tabular-nums">{value}</span>
      {sub && <span className="text-xs text-base-content/45">{sub}</span>}
    </Card>
  );
}

// The band's data seam. Same rule as SummaryBand: this only mounts when the
// infra-metrics module is active, so the infra endpoints are never called on an
// install without the sensor. A cluster with no nodes reporting yet renders
// nothing rather than a row of zeroes claiming an empty estate.
export function CapacityBandLive() {
  const { time } = useTimeRange();
  const { data: nodesData, isLoading } = useNodesData(time);
  const { data: podsData } = usePodsData(time);
  const { data: zonesData } = useZoneTraffic(time);

  if (isLoading) return <CenteredSpinner />;
  const nodes = nodesData?.nodes ?? [];
  if (!nodes.length) return null;

  return (
    <CapacityBand nodes={nodes} pods={podsData?.pods ?? []} zones={zonesData?.zones ?? []} />
  );
}

export function CapacityBand({
  nodes,
  pods,
  zones = [],
}: {
  nodes: NodeStats[];
  pods: PodStats[];
  zones?: ZoneTraffic[];
}) {
  const totals = useMemo(() => {
    const memUsed = nodes.reduce((a, n) => a + n.memoryUsageBytes, 0);
    const memTotal = nodes.reduce((a, n) => a + n.memoryUsageBytes + n.memoryAvailableBytes, 0);
    return {
      cores: nodes.reduce((a, n) => a + n.cpuUsageCores, 0),
      memUsed,
      memTotal,
      net: nodes.reduce((a, n) => a + n.networkRxBytesPerSec + n.networkTxBytesPerSec, 0),
      pods: nodes.reduce((a, n) => a + n.podCount, 0),
      namespaces: new Set(pods.map((p) => p.namespace)).size,
      workloads: new Set(pods.map((p) => p.workload).filter(Boolean)).size,
    };
  }, [nodes, pods]);

  // Busiest first: the node about to run out of memory is the one worth the row.
  const bars = useMemo(
    () =>
      [...nodes]
        .map((n) => {
          const total = n.memoryUsageBytes + n.memoryAvailableBytes;
          return { node: n, total, ratio: total > 0 ? n.memoryUsageBytes / total : 0 };
        })
        .sort((a, b) => b.ratio - a.ratio),
    [nodes],
  );
  const shown = bars.slice(0, MAX_BARS);

  return (
    <section className="flex flex-col gap-2" data-testid="dashboard-capacity">
      <div className="flex items-center gap-2 border-b border-neutral pb-1.5">
        <h2 className="text-sm font-semibold">Kubernetes capacity</h2>
        <span className="text-xs text-base-content/45">
          from the node and pod metrics the sensor collects
        </span>
        <Link href="/nodes" className="ml-auto text-xs text-primary hover:underline">
          All nodes →
        </Link>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <StatTile label="Nodes" value={String(nodes.length)} />
        <StatTile label="Pods" value={String(totals.pods)} />
        <StatTile label="Namespaces" value={String(totals.namespaces)} />
        <StatTile label="Workloads" value={String(totals.workloads)} />
        <StatTile label="CPU in use" value={`${formatCores(totals.cores)} cores`} />
        <StatTile
          label="Memory"
          value={formatBytes(totals.memUsed)}
          sub={totals.memTotal > 0 ? `of ${formatBytes(totals.memTotal)}` : undefined}
        />
      </div>

      {shown.length > 0 && (
        <Card className="flex flex-col gap-2.5 p-3">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-base-content/70">
              Memory utilization per node
            </span>
            <span className="text-xs text-base-content/45">
              {formatBytes(totals.net)}/s network across the estate
            </span>
          </div>
          {shown.map(({ node, total, ratio }) => (
            <div key={node.name} className="flex items-center gap-3 text-xs">
              <span className="w-40 shrink-0 truncate font-medium text-primary" title={node.name}>
                {node.name}
              </span>
              <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-base-300">
                <div
                  className={cn("h-full rounded-full transition-[width]", barTone(ratio))}
                  style={{ width: `${Math.min(100, Math.max(2, ratio * 100)).toFixed(1)}%` }}
                />
              </div>
              <span className="w-12 shrink-0 text-right tabular-nums">
                {(ratio * 100).toFixed(0)}%
              </span>
              <span className="w-32 shrink-0 text-right tabular-nums text-base-content/45">
                {formatBytes(node.memoryUsageBytes)} / {formatBytes(total)}
              </span>
            </div>
          ))}
          {bars.length > shown.length && (
            <Link href="/nodes" className="text-xs text-primary hover:underline">
              +{bars.length - shown.length} more nodes →
            </Link>
          )}
        </Card>
      )}

      <ZoneCrossings zones={zones} />
    </section>
  );
}

// Cross-zone traffic, when the sensor is counting it. No rows means no card —
// the same rule as the band above: accounting is opt-in, a single-zone cluster
// never produces a crossing, and an empty table would read as "no cross-zone
// traffic" when the truthful answer is "nobody is measuring".
//
// Bytes, not currency. What a crossing costs depends on the cloud, the
// direction and the contract; naming a number here would be a guess wearing a
// measurement's clothes. The measurement ships; a price factor is config.
function ZoneCrossings({ zones }: { zones: ZoneTraffic[] }) {
  const total = zones.reduce((a, z) => a + z.bytes, 0);
  if (!zones.length || total <= 0) return null;
  const shown = zones.slice(0, MAX_ZONE_PAIRS);

  return (
    <Card className="flex flex-col gap-2.5 p-3" data-testid="zone-crossings">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-base-content/70">Cross-zone traffic</span>
        <span className="text-xs text-base-content/45">
          {formatBytes(total)} over the window, measured in the kernel
        </span>
      </div>
      {shown.map((z) => (
        <div key={`${z.srcZone}->${z.dstZone}`} className="flex items-center gap-3 text-xs">
          <span className="w-40 shrink-0 truncate font-medium" title={z.srcZone}>
            {z.srcZone}
          </span>
          <span className="shrink-0 text-base-content/45" aria-label="to">
            →
          </span>
          <span className="flex-1 truncate font-medium" title={z.dstZone}>
            {z.dstZone}
          </span>
          <span className="w-24 shrink-0 text-right tabular-nums">{formatBytes(z.bytes)}</span>
        </div>
      ))}
      {zones.length > shown.length && (
        <span className="text-xs text-base-content/45">
          +{zones.length - shown.length} more zone pairs
        </span>
      )}
    </Card>
  );
}
