"use client";

import { useEffect, useMemo } from "react";
import { Flame } from "lucide-react";
import { Select } from "@/components/ui/select";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useFlamegraph, useProfiledServices } from "@/hooks/use-profiling-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { FlameGraph } from "./flame-graph";

// Continuous CPU profiling: pick a profiled service, read its aggregated
// flame graph for the window. Selection lives in the URL.
export function ProfilingScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const service = get("service") ?? "";

  const services = useProfiledServices(time);
  const list = useMemo(() => services.data?.services ?? [], [services.data]);

  // Default to the busiest profiled service once the list arrives.
  useEffect(() => {
    if (!service && list.length) {
      setMany({ service: list[0].name });
    }
  }, [service, list, setMany]);

  const flame = useFlamegraph(time, service);

  if (services.isLoading) return <CenteredSpinner />;

  if (!list.length) {
    return (
      <EmptyState icon={Flame} title="No profiling data yet">
        The sensor&apos;s profiler container samples CPU stacks cluster-wide
        (kernel ≥ 5.8 with BTF). Data appears within a minute of installing —
        the OTLP Profiles signal is alpha, so expect rough edges.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Select
          ariaLabel="Profiled service"
          className="w-64"
          value={service}
          onChange={(v) => setMany({ service: v || undefined })}
          options={list.map((s) => ({
            value: s.name,
            label: `${s.name} (${s.samples.toLocaleString()} samples)`,
          }))}
        />
        <p className="text-xs text-base-content/55">
          CPU stacks aggregated over the selected window · widths are sample
          shares, not time.
        </p>
      </div>
      {flame.isLoading ? (
        <CenteredSpinner />
      ) : flame.data && flame.data.root.value > 0 ? (
        <FlameGraph root={flame.data.root} />
      ) : (
        <p className="text-sm text-base-content/60">
          No samples for {service} in this window.
        </p>
      )}
    </div>
  );
}
