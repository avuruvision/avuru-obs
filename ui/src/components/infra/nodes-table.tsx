"use client";

import { useMemo } from "react";
import { Card } from "@/components/ui/card";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/cn";
import { Sparkline } from "./sparkline";
import type { NodeStats } from "@/lib/api-types";

function formatCores(cores: number): string {
  return cores >= 10 ? cores.toFixed(1) : cores.toFixed(2);
}

// rx and tx are shown as one cell, so they sort as one number. Sorting on rx
// alone would silently rank a tx-heavy node as quiet.
type SortableNode = NodeStats & { networkTotalBytesPerSec: number };
type SortKey = "name" | "cpuUsageCores" | "memoryUsageBytes" | "networkTotalBytesPerSec" | "podCount";

const CPU: SortColumn<SortKey> = { key: "cpuUsageCores", label: "CPU (cores)", numeric: true };
const MEM: SortColumn<SortKey> = { key: "memoryUsageBytes", label: "Memory", numeric: true };
const NET: SortColumn<SortKey> = { key: "networkTotalBytesPerSec", label: "Net rx/tx", numeric: true };
const PODS: SortColumn<SortKey> = { key: "podCount", label: "Pods", numeric: true };
const NAME: SortColumn<SortKey> = { key: "name", label: "Node" };

// Per-node utilization table; selecting a node filters the pods panel.
// Defaults to name order — the roster a reader expects to stay put between
// refreshes. Ranking by load is one click on any numeric column.
export function NodesTable({
  nodes,
  selected,
  onSelect,
}: {
  nodes: NodeStats[];
  selected?: string;
  onSelect: (node?: string) => void;
}) {
  const sort = useColumnSort<SortKey>("name", true);

  const rows = useMemo(
    () =>
      sort.sortRows<SortableNode>(
        nodes.map((n) => ({
          ...n,
          networkTotalBytesPerSec: n.networkRxBytesPerSec + n.networkTxBytesPerSec,
        })),
      ),
    [nodes, sort],
  );

  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table className="table-dense w-full text-sm">
          <thead>
            <tr className="border-b border-neutral text-left">
              <SortableTh col={NAME} sort={sort} />
              <SortableTh col={CPU} sort={sort} />
              {/* Trends are sparklines, not scalars — nothing to order by. */}
              <th className="text-right">CPU trend</th>
              <SortableTh col={MEM} sort={sort} />
              <th className="text-right">Mem trend</th>
              <SortableTh col={NET} sort={sort} />
              <SortableTh col={PODS} sort={sort} />
            </tr>
          </thead>
          <tbody>
            {rows.map((n) => {
              const isSelected = selected === n.name;
              const memTotal = n.memoryUsageBytes + n.memoryAvailableBytes;
              return (
                <tr
                  key={n.name}
                  onClick={() => onSelect(isSelected ? undefined : n.name)}
                  className={cn(
                    "cursor-pointer border-b border-neutral/40 transition-colors last:border-0",
                    isSelected ? "bg-primary/10" : "hover:bg-base-300/50",
                  )}
                  title={isSelected ? "Show pods on all nodes" : `Show pods on ${n.name}`}
                >
                  <td className="font-medium text-primary">{n.name}</td>
                  <td className="text-right font-mono text-xs">{formatCores(n.cpuUsageCores)}</td>
                  <td className="text-right">
                    <Sparkline points={n.cpuSeries} />
                  </td>
                  <td className="text-right font-mono text-xs">
                    {formatBytes(n.memoryUsageBytes)}
                    {memTotal > n.memoryUsageBytes && (
                      <span className="text-base-content/40"> / {formatBytes(memTotal)}</span>
                    )}
                  </td>
                  <td className="text-right">
                    <Sparkline points={n.memorySeries} className="text-secondary" />
                  </td>
                  <td className="text-right font-mono text-xs">
                    {formatBytes(n.networkRxBytesPerSec)}/s · {formatBytes(n.networkTxBytesPerSec)}/s
                  </td>
                  <td className="text-right font-mono text-xs">{n.podCount}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
