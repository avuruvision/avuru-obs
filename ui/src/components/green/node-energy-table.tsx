"use client";

import { formatWh } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { GreenNodeEnergy } from "@/lib/api-types";
import { QualityBadge } from "./quality-badge";

// Per-node energy with the coverage tier — the detail behind the coverage
// tiles: WHICH node is absent or estimated, not just how many. The hub sends
// every known node (absent ones at 0 Wh), heaviest first; a handful of rows,
// so no client-side sorting like the service table.
export function NodeEnergyTable({ nodes }: { nodes: GreenNodeEnergy[] }) {
  return (
    <div className="overflow-x-auto border-t border-neutral">
      <table data-testid="node-energy-table" className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            <th>Node</th>
            <th className="text-right">Energy</th>
            <th className="text-right">Quality</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => {
            const absent = n.quality === "absent";
            return (
              <tr key={n.node} className="border-b border-neutral/40 last:border-0">
                <td className={cn("font-medium", absent && "text-base-content/50")}>{n.node}</td>
                <td className="text-right font-mono text-xs">{formatWh(n.wh)}</td>
                <td className="text-right">
                  <NodeQuality node={n} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// The estimated share reuses the screen-level badge (same ±30-50% tooltip); a
// fully-measured node states its tier in plain muted text, an absent node its
// gap, and unlabeled energy (quality "") renders a dash rather than a claim.
function NodeQuality({ node }: { node: GreenNodeEnergy }) {
  const share = node.wh > 0 ? (node.estimatedWh ?? 0) / node.wh : 0;
  if (share > 0) return <QualityBadge estimatedShare={share} />;
  if (node.quality === "measured" || node.quality === "absent") {
    return <span className="text-xs text-base-content/50">{node.quality}</span>;
  }
  return <span className="text-xs text-base-content/35">—</span>;
}
