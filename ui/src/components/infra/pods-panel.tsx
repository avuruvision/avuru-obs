"use client";

import { Card } from "@/components/ui/card";
import { formatBytes } from "@/lib/format";
import type { PodStats } from "@/lib/api-types";

// Busiest pods (optionally scoped to one node), latest CPU/memory.
export function PodsPanel({ pods, node }: { pods: PodStats[]; node?: string }) {
  if (!pods.length) {
    return (
      <Card className="p-6 text-center text-sm text-base-content/60">
        No pod metrics {node ? `for ${node} ` : ""}in this window.
      </Card>
    );
  }
  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Pod</th>
            <th>Namespace</th>
            <th>Workload</th>
            {!node && <th>Node</th>}
            <th className="text-right">CPU (cores)</th>
            <th className="text-right">Memory</th>
          </tr>
        </thead>
        <tbody>
          {pods.map((p) => (
            <tr
              key={`${p.namespace}/${p.name}`}
              className="border-b border-neutral/40 last:border-0"
            >
              <td className="max-w-64 truncate font-mono text-xs">{p.name}</td>
              <td className="text-xs">{p.namespace}</td>
              <td className="text-xs">{p.workload ?? <span className="text-base-content/40">—</span>}</td>
              {!node && <td className="text-xs">{p.node}</td>}
              <td className="text-right font-mono text-xs">{p.cpuUsageCores.toFixed(3)}</td>
              <td className="text-right font-mono text-xs">{formatBytes(p.memoryUsageBytes)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
