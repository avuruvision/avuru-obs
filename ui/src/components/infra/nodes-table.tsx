"use client";

import { Card } from "@/components/ui/card";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/cn";
import { Sparkline } from "./sparkline";
import type { NodeStats } from "@/lib/api-types";

function formatCores(cores: number): string {
  return cores >= 10 ? cores.toFixed(1) : cores.toFixed(2);
}

// Per-node utilization table; selecting a node filters the pods panel.
export function NodesTable({
  nodes,
  selected,
  onSelect,
}: {
  nodes: NodeStats[];
  selected?: string;
  onSelect: (node?: string) => void;
}) {
  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Node</th>
            <th className="text-right">CPU (cores)</th>
            <th className="text-right">CPU trend</th>
            <th className="text-right">Memory</th>
            <th className="text-right">Mem trend</th>
            <th className="text-right">Net rx/tx</th>
            <th className="text-right">Pods</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => {
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
    </Card>
  );
}
