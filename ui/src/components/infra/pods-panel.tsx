"use client";

import { useMemo } from "react";
import { Card } from "@/components/ui/card";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatBytes } from "@/lib/format";
import type { PodStats } from "@/lib/api-types";

type SortKey = "name" | "namespace" | "workload" | "node" | "cpuUsageCores" | "memoryUsageBytes";

const NAME: SortColumn<SortKey> = { key: "name", label: "Pod" };
const NAMESPACE: SortColumn<SortKey> = { key: "namespace", label: "Namespace" };
const WORKLOAD: SortColumn<SortKey> = { key: "workload", label: "Workload" };
const NODE: SortColumn<SortKey> = { key: "node", label: "Node" };
const CPU: SortColumn<SortKey> = { key: "cpuUsageCores", label: "CPU (cores)", numeric: true };
const MEM: SortColumn<SortKey> = { key: "memoryUsageBytes", label: "Memory", numeric: true };

// Pods (optionally scoped to one node), latest CPU/memory. Defaults to CPU
// descending, which is the order the hub already returns and what the heading
// above has always promised — sorting makes that a choice rather than a fact.
//
// `filtered` distinguishes "nothing collected" from "nothing matched": the
// first is a setup problem, the second is the filter the reader just typed,
// and telling them apart is the difference between a useful empty state and a
// misleading one.
export function PodsPanel({
  pods,
  node,
  filtered = false,
}: {
  pods: PodStats[];
  node?: string;
  filtered?: boolean;
}) {
  const sort = useColumnSort<SortKey>("cpuUsageCores");
  const rows = useMemo(() => sort.sortRows(pods), [pods, sort]);

  if (!pods.length) {
    return (
      <Card className="p-6 text-center text-sm text-base-content/60">
        {filtered
          ? "No pods match this filter."
          : `No pod metrics ${node ? `for ${node} ` : ""}in this window.`}
      </Card>
    );
  }
  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table className="table-dense w-full text-sm">
          <thead>
            <tr className="border-b border-neutral text-left">
              <SortableTh col={NAME} sort={sort} />
              <SortableTh col={NAMESPACE} sort={sort} />
              <SortableTh col={WORKLOAD} sort={sort} />
              {!node && <SortableTh col={NODE} sort={sort} />}
              <SortableTh col={CPU} sort={sort} />
              <SortableTh col={MEM} sort={sort} />
            </tr>
          </thead>
          <tbody>
            {rows.map((p) => (
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
      </div>
    </Card>
  );
}
