"use client";

import { useMemo, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Sparkline } from "@/components/infra/sparkline";
import { formatGco2e, formatMgPerReq, formatWh } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { GreenServiceEnergy } from "@/lib/api-types";

type SortKey = "service" | "wh" | "gco2e" | "requests" | "mgCO2ePerRequest";

const COLUMNS: { key: SortKey; label: string; numeric?: boolean }[] = [
  { key: "service", label: "Service" },
  { key: "wh", label: "Energy", numeric: true },
  { key: "gco2e", label: "Carbon", numeric: true },
  { key: "requests", label: "Requests", numeric: true },
  { key: "mgCO2ePerRequest", label: "mgCO2e/req", numeric: true },
];

// The synthetic roll-up rows the hub appends last (green.go): the folded tail
// and the coverage-gap bucket. They are pinned below the real services and
// muted so the coverage story stays legible — never sorted into the ranking.
const SYNTHETIC = new Set(["(other)", "(unattributed)"]);

// Per-service energy & carbon, sortable client-side (one window's worth of
// rows, never paginated) — mirrors ServicesTable. The trend column reuses the
// infra Sparkline over each row's Wh series.
export function ServiceEnergyTable({ services }: { services: GreenServiceEnergy[] }) {
  const [sortKey, setSortKey] = useState<SortKey>("wh");
  const [sortAsc, setSortAsc] = useState(false);

  const sorted = useMemo(() => {
    const real = services.filter((s) => !SYNTHETIC.has(s.service));
    const synthetic = services.filter((s) => SYNTHETIC.has(s.service));
    real.sort((a, b) => {
      const av = a[sortKey];
      const bv = b[sortKey];
      const cmp =
        typeof av === "string" && typeof bv === "string"
          ? av.localeCompare(bv)
          : Number(av ?? 0) - Number(bv ?? 0);
      return sortAsc ? cmp : -cmp;
    });
    return [...real, ...synthetic];
  }, [services, sortKey, sortAsc]);

  const toggleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortAsc((v) => !v);
    } else {
      setSortKey(key);
      setSortAsc(key === "service");
    }
  };

  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table className="table-dense w-full text-sm">
          <thead>
            <tr className="border-b border-neutral text-left">
              {COLUMNS.map((c) => (
                <th
                  key={c.key}
                  className={cn(c.numeric && "text-right")}
                  aria-sort={
                    sortKey === c.key ? (sortAsc ? "ascending" : "descending") : undefined
                  }
                >
                  <button
                    type="button"
                    onClick={() => toggleSort(c.key)}
                    className={cn(
                      "inline-flex items-center gap-1 hover:text-base-content",
                      c.numeric && "flex-row-reverse",
                    )}
                  >
                    {c.label}
                    {sortKey === c.key &&
                      (sortAsc ? (
                        <ArrowUp className="h-3 w-3" aria-hidden />
                      ) : (
                        <ArrowDown className="h-3 w-3" aria-hidden />
                      ))}
                  </button>
                </th>
              ))}
              <th className="text-right">Trend</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((s) => {
              const synthetic = SYNTHETIC.has(s.service);
              return (
                <tr
                  key={s.service}
                  className={cn(
                    "border-b border-neutral/40 last:border-0",
                    synthetic && "text-base-content/50 italic",
                  )}
                >
                  <td className={cn("font-medium", synthetic ? "" : "text-primary")}>
                    {s.service}
                  </td>
                  <td className="text-right font-mono text-xs">{formatWh(s.wh)}</td>
                  <td className="text-right font-mono text-xs">{formatGco2e(s.gco2e)}</td>
                  <td className="text-right font-mono text-xs">
                    {s.requests ? s.requests.toLocaleString() : "—"}
                  </td>
                  <td className="text-right font-mono text-xs">
                    {formatMgPerReq(s.mgCO2ePerRequest)}
                  </td>
                  <td className="text-right">
                    <div className="flex justify-end">
                      <Sparkline
                        points={(s.points ?? []).map((p) => ({ time: p.time, value: p.wh }))}
                        className={synthetic ? "text-base-content/30" : "text-success"}
                      />
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
