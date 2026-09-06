"use client";

import { useMemo } from "react";
import Link from "next/link";
import { Card } from "@/components/ui/card";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatMs, formatPercent } from "@/lib/format";
import type { ServiceEdge } from "@/lib/api-types";

type SortKey = "source" | "target" | "calls" | "successRate" | "p95Ms" | "hops";

// hops is the length of viaTransport: a dependency crossing three proxies is a
// different animal to one crossing this proxy alone, and on an ambient install
// the three-hop path (ztunnel → waypoint → ztunnel) is the normal one.
type CarriedRow = ServiceEdge & { successRate: number; hops: number };

const SOURCE: SortColumn<SortKey> = { key: "source", label: "Caller" };
const TARGET: SortColumn<SortKey> = { key: "target", label: "Callee" };
const CALLS: SortColumn<SortKey> = { key: "calls", label: "Calls", numeric: true };
const SUCCESS: SortColumn<SortKey> = { key: "successRate", label: "Success", numeric: true };
const P95: SortColumn<SortKey> = { key: "p95Ms", label: "p95", numeric: true };
const HOPS: SortColumn<SortKey> = { key: "hops", label: "Hops", numeric: true };

export function CarriedTable({ edges }: { edges: ServiceEdge[] }) {
  const sort = useColumnSort<SortKey>("calls", false);

  const rows = useMemo(
    () =>
      sort.sortRows<CarriedRow>(
        edges.map((e) => ({
          ...e,
          successRate: 1 - e.errorRate,
          hops: e.viaTransport?.length ?? 0,
        })),
      ),
    [edges, sort],
  );

  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table data-testid="mesh-carried" className="table-dense w-full text-sm">
          <thead className="text-xs text-base-content/55">
            <tr className="border-b border-neutral text-left">
              <SortableTh col={SOURCE} sort={sort} />
              <SortableTh col={TARGET} sort={sort} />
              <SortableTh col={CALLS} sort={sort} iconFirst />
              <SortableTh col={SUCCESS} sort={sort} iconFirst />
              <SortableTh col={P95} sort={sort} iconFirst />
              <SortableTh col={HOPS} sort={sort} iconFirst />
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={`${e.source}->${e.target}`} className="border-b border-neutral/50 last:border-0">
                <td>
                  <ServiceLink name={e.source} />
                </td>
                <td>
                  <ServiceLink name={e.target} />
                </td>
                <td className="text-right tabular-nums">{e.calls.toLocaleString()}</td>
                <td className={`text-right tabular-nums ${e.errorRate > 0 ? "text-error" : ""}`}>
                  {formatPercent(e.successRate)}
                </td>
                {/* Flow-derived edges have no span to measure, so p95 is absent
                    rather than zero. */}
                <td className="text-right tabular-nums">
                  {e.p95Ms === undefined ? "—" : formatMs(e.p95Ms)}
                </td>
                <td
                  className="text-right tabular-nums"
                  title={e.viaTransport?.join(" → ")}
                >
                  {e.hops}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function ServiceLink({ name }: { name: string }) {
  return (
    <Link
      href={`/services?service=${encodeURIComponent(name)}`}
      className="font-medium hover:text-primary hover:underline"
    >
      {name}
    </Link>
  );
}
