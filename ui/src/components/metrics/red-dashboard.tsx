"use client";

import { Gauge } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useRedData } from "@/hooks/use-red-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { RedCard } from "./red-card";

// RED metrics dashboard: busiest services of the window (or one filtered
// service), derived from the same spans as the trace explorer — no separate
// metrics system to operate.
export function RedDashboard() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const service = get("service") ?? undefined;
  const includeAux = get("includeAux") === "true";

  const { data, isLoading } = useRedData(time, service, includeAux);

  if (isLoading) return <CenteredSpinner />;
  const series = data?.series ?? [];

  if (!series.length) {
    return (
      <EmptyState icon={Gauge} title="No request metrics yet">
        RED metrics derive from spans — they appear as soon as services are
        traced (zero-code via the sensor, or apps sending OTLP).
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <input
            type="text"
            defaultValue={service ?? ""}
            placeholder="service (empty = busiest)"
            aria-label="Filter by service"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setMany({ service: (e.target as HTMLInputElement).value || undefined });
              }
            }}
            className="h-9 w-56 rounded-lg border border-neutral bg-base-200 px-3 text-sm outline-none placeholder:text-base-content/40"
          />
          <p className="text-xs text-base-content/55">
            {series.length} services · rate, errors & duration from entry spans.
          </p>
        </div>
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
          <input
            type="checkbox"
            checked={includeAux}
            onChange={(e) => setMany({ includeAux: e.target.checked ? "true" : undefined })}
            className="accent-primary"
          />
          Show auxiliary requests
        </label>
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {series.map((s) => (
          <RedCard key={s.service} series={s} />
        ))}
      </div>
    </div>
  );
}
