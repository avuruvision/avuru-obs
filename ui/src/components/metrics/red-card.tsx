"use client";

import Link from "next/link";
import { Card } from "@/components/ui/card";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { RedChart } from "./red-chart";
import type { RedSeries } from "@/lib/api-types";

// One service's RED over time: rate, error % and latency percentiles.
export function RedCard({ series }: { series: RedSeries }) {
  const pts = series.points;
  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-center justify-between">
        <h3 className="truncate text-sm font-semibold text-primary">{series.service}</h3>
        <Link
          href={`/traces?service=${encodeURIComponent(series.service)}`}
          className="text-xs text-base-content/50 hover:text-primary hover:underline"
        >
          traces →
        </Link>
      </div>
      <RedChart
        title="Rate"
        format={formatRate}
        series={[{ label: "req rate", values: pts.map((p) => p.ratePerSec), className: "text-primary" }]}
      />
      <RedChart
        title="Errors"
        format={formatPercent}
        series={[{ label: "error rate", values: pts.map((p) => p.errorRate), className: "text-error" }]}
      />
      <RedChart
        title="Duration"
        format={formatMs}
        series={[
          { label: "p50", values: pts.map((p) => p.p50Ms), className: "text-secondary" },
          { label: "p95", values: pts.map((p) => p.p95Ms), className: "text-warning" },
          { label: "p99", values: pts.map((p) => p.p99Ms), className: "text-error" },
        ]}
      />
    </Card>
  );
}
