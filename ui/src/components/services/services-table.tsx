"use client";

import { useMemo } from "react";
import { useRouter } from "next/navigation";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import type { ServiceStats } from "@/lib/api-types";

type SortKey = "name" | "ratePerSec" | "errorRate" | "p50Ms" | "p95Ms" | "p99Ms";

const COLUMNS: SortColumn<SortKey>[] = [
  { key: "name", label: "Service" },
  { key: "ratePerSec", label: "Rate", numeric: true },
  { key: "errorRate", label: "Errors", numeric: true },
  { key: "p50Ms", label: "p50", numeric: true },
  { key: "p95Ms", label: "p95", numeric: true },
  { key: "p99Ms", label: "p99", numeric: true },
];

// RED inventory table — row click opens the service's traces. Sorting is
// client-side: the list is one page (services in the window), never paginated.
export function ServicesTable({ services }: { services: ServiceStats[] }) {
  const router = useRouter();
  const sort = useColumnSort<SortKey>("ratePerSec");
  const sorted = useMemo(() => sort.sortRows(services), [services, sort]);

  return (
    <Card className="overflow-hidden">
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left">
            {COLUMNS.map((c) => (
              <SortableTh key={c.key} col={c} sort={sort} />
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.map((s) => (
            <tr
              key={s.name}
              onClick={() => router.push(`/services?service=${encodeURIComponent(s.name)}`)}
              className="cursor-pointer border-b border-neutral/40 transition-colors last:border-0 hover:bg-base-300/50"
              title={`Open ${s.name} traces`}
            >
              <td className="font-medium text-primary">{s.name}</td>
              <td className="text-right font-mono text-xs">{formatRate(s.ratePerSec)}</td>
              <td className="text-right">
                {s.errorRate > 0 ? (
                  <Badge tone="error">{formatPercent(s.errorRate)}</Badge>
                ) : (
                  <span className="text-base-content/40">—</span>
                )}
              </td>
              <td className="text-right font-mono text-xs">{formatMs(s.p50Ms)}</td>
              <td className="text-right font-mono text-xs">{formatMs(s.p95Ms)}</td>
              <td className="text-right font-mono text-xs">{formatMs(s.p99Ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
