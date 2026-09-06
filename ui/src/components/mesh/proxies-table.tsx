"use client";

import { useMemo } from "react";
import { Badge } from "@/components/ui/badge";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { roleLabel } from "./mesh-roles";
import type { MeshProxy } from "@/lib/api-types";

type SortKey =
  | "name"
  | "namespace"
  | "role"
  | "ratePerSec"
  | "successRate"
  | "p50Ms"
  | "p95Ms"
  | "callsIn"
  | "callsOut";

// Sorted on SUCCESS, not on the error rate the API sends, so the column sorts
// the way it reads: one click puts the worst proxy on top.
type SortableProxy = MeshProxy & { successRate: number };

const NAME: SortColumn<SortKey> = { key: "name", label: "Workload" };
const NAMESPACE: SortColumn<SortKey> = { key: "namespace", label: "Namespace" };
const ROLE: SortColumn<SortKey> = { key: "role", label: "Role" };
const RATE: SortColumn<SortKey> = { key: "ratePerSec", label: "Rate", numeric: true };
const SUCCESS: SortColumn<SortKey> = { key: "successRate", label: "Success", numeric: true };
const P50: SortColumn<SortKey> = { key: "p50Ms", label: "p50", numeric: true };
const P95: SortColumn<SortKey> = { key: "p95Ms", label: "p95", numeric: true };
// "Calls", not "Carried": these are call counts. The columns said bytes for two
// releases while rendering counts, which is the kind of quiet lie that gets a
// capacity decision made on the wrong number.
const IN: SortColumn<SortKey> = { key: "callsIn", label: "Calls in", numeric: true };
const OUT: SortColumn<SortKey> = { key: "callsOut", label: "Calls out", numeric: true };

export function ProxiesTable({ proxies }: { proxies: MeshProxy[] }) {
  // Worst success first: the reason to open this screen is that something is
  // wrong, and the fleet is too long to scan by hand.
  const sort = useColumnSort<SortKey>("successRate", true);

  const rows = useMemo(
    () => sort.sortRows<SortableProxy>(proxies.map((p) => ({ ...p, successRate: 1 - p.errorRate }))),
    [proxies, sort],
  );

  return (
    <div className="overflow-x-auto">
      <table data-testid="mesh-proxies" className="table-dense w-full text-sm">
        <thead className="text-xs text-base-content/55">
          <tr className="border-b border-neutral text-left">
            <SortableTh col={NAME} sort={sort} />
            <SortableTh col={NAMESPACE} sort={sort} />
            <SortableTh col={ROLE} sort={sort} />
            <SortableTh col={RATE} sort={sort} iconFirst />
            <SortableTh col={SUCCESS} sort={sort} iconFirst />
            <SortableTh col={P50} sort={sort} iconFirst />
            <SortableTh col={P95} sort={sort} iconFirst />
            <SortableTh col={IN} sort={sort} iconFirst />
            <SortableTh col={OUT} sort={sort} iconFirst />
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <ProxyRow key={p.name} proxy={p} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ProxyRow({ proxy }: { proxy: SortableProxy }) {
  // Traffic in with none coming out is a proxy that stopped forwarding — a
  // failure its own error rate can miss entirely, which is why the two numbers
  // sit side by side rather than being summed into "throughput".
  const stalled = proxy.callsIn > 0 && proxy.callsOut === 0;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium">{proxy.name}</td>
      <td className="text-base-content/70">{proxy.namespace ?? "—"}</td>
      <td>
        {proxy.role ? (
          <Badge tone={proxy.role === "control-plane" ? "primary" : "neutral"}>
            {roleLabel(proxy.role)}
          </Badge>
        ) : (
          // Nothing named it and the mesh labelled nothing on it. Saying so is
          // more useful than a role invented to fill the cell.
          <span className="text-base-content/40" title="No role could be established for this proxy">
            —
          </span>
        )}
      </td>
      <td className="text-right tabular-nums">{formatRate(proxy.ratePerSec)}</td>
      <td className={`text-right tabular-nums ${proxy.errorRate > 0 ? "text-error" : ""}`}>
        {formatPercent(proxy.successRate)}
      </td>
      <td className="text-right tabular-nums">{formatMs(proxy.p50Ms)}</td>
      <td className="text-right tabular-nums">{formatMs(proxy.p95Ms)}</td>
      <td className="text-right tabular-nums">{proxy.callsIn.toLocaleString()}</td>
      <td
        className={`text-right tabular-nums ${stalled ? "text-warning" : ""}`}
        title={stalled ? "Traffic arriving, nothing forwarded" : undefined}
      >
        {proxy.callsOut.toLocaleString()}
      </td>
    </tr>
  );
}
