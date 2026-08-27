"use client";

import Link from "next/link";
import { useMemo } from "react";
import { ArrowLeft, Boxes, Map as MapIcon } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { RedChart } from "@/components/metrics/red-chart";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useCapabilities } from "@/hooks/use-capabilities";
import { useRedData } from "@/hooks/use-red-data";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { statusDotClass, statusLabel, statusTone } from "@/lib/health-status";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { ServiceDependencies } from "./service-dependencies";
import { ServiceSignals, type SignalTab } from "./service-signals";

const SIGNAL_TABS: SignalTab[] = ["overview", "traces", "logs", "errors"];

// Everything one service is doing, in one place.
//
// It is composed from the reads the other screens already make rather than a
// per-service endpoint of its own: the dependency half in particular MUST come
// from the map's edge set, because that response is where transport
// classification and hop collapse are applied. A second query would eventually
// disagree with the map about what depends on what, and the two screens would
// be telling different stories about the same estate.
export function ServiceDetail({ service }: { service: string }) {
  const { time, windowMs } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data: caps } = useCapabilities();

  const rawTab = get("view");
  const tab: SignalTab = SIGNAL_TABS.includes(rawTab as SignalTab)
    ? (rawTab as SignalTab)
    : "overview";

  const map = useServiceMapData(time, includeAux);
  const red = useRedData(time, service, includeAux);
  // The health board is the authority on status; without the module there is
  // simply no status to show, which is why this is gated rather than defaulted.
  const healthOn = caps?.modules.includes("service-health") ?? false;
  const { byService: health } = useServiceHealthStatus(time, includeAux, healthOn);

  const stats = useMemo(
    () => map.data?.services.find((s) => s.name === service),
    [map.data, service],
  );
  const edges = map.data?.edges ?? [];
  const status = health.get(service);
  const points = red.data?.series.find((s) => s.service === service)?.points ?? [];

  const openService = (name: string) => setMany({ service: name, view: undefined });

  if (map.isLoading) return <CenteredSpinner />;

  // A name with no service behind it in this window: a stale bookmark, or a
  // service that stopped reporting. Say which, rather than rendering empty
  // cards that read as "this service does nothing".
  if (!stats) {
    return (
      <div className="flex flex-col gap-3">
        <BackLink />
        <EmptyState icon={Boxes} title={`No data for ${service}`}>
          Nothing was observed from this service in the selected window. It may
          have stopped reporting, or the window may predate it — try widening
          the time range.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <BackLink />
          <h1 className="truncate font-mono text-lg font-semibold">{service}</h1>
          {status && (
            <Badge tone={statusTone(status.status)}>
              <span
                className={`mr-1 inline-block h-2 w-2 rounded-full ${statusDotClass(status.status)}`}
                aria-hidden
              />
              {statusLabel(status.status)}
            </Badge>
          )}
          {stats.namespace && (
            <span className="rounded bg-base-300 px-1.5 py-0.5 text-[11px] text-base-content/60">
              {stats.namespace}
            </span>
          )}
          {stats.role && (
            <span className="rounded bg-base-300 px-1.5 py-0.5 text-[11px] text-base-content/60">
              {stats.role}
            </span>
          )}
        </div>
        <Link
          href={`/service-map?q=${encodeURIComponent(service)}`}
          className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
        >
          <MapIcon className="h-3 w-3" aria-hidden /> show on the map
        </Link>
      </div>

      {status?.reason && (
        <p className="text-xs text-base-content/60">{status.reason}</p>
      )}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Rate" value={formatRate(stats.ratePerSec)} />
        <Stat
          label="Errors"
          value={formatPercent(stats.errorRate)}
          tone={stats.errorRate > 0 ? "error" : undefined}
        />
        <Stat label="p95" value={formatMs(stats.p95Ms)} />
        <Stat label="p99" value={formatMs(stats.p99Ms)} />
      </div>

      <Tabs<SignalTab>
        items={[
          { value: "overview", label: "Overview" },
          { value: "traces", label: "Traces" },
          ...(caps?.modules.includes("logs") ? [{ value: "logs" as const, label: "Logs" }] : []),
          ...(caps?.modules.includes("error-tracking")
            ? [{ value: "errors" as const, label: "Errors" }]
            : []),
        ]}
        value={tab}
        onChange={(v) => setMany({ view: v === "overview" ? undefined : v })}
      />

      {tab === "overview" ? (
        <div className="flex flex-col gap-3">
          <ServiceDependencies
            service={service}
            edges={edges}
            windowMs={windowMs}
            onSelect={openService}
          />
          <Card className="flex flex-col gap-3 p-4">
            <h2 className="text-sm font-semibold text-primary">Rate, errors and duration</h2>
            {red.isLoading ? (
              <CenteredSpinner />
            ) : points.length === 0 ? (
              <p className="text-xs text-base-content/45">
                No RED series for this window.
              </p>
            ) : (
              <>
                <RedChart
                  title="Rate"
                  format={formatRate}
                  series={[
                    {
                      label: "req rate",
                      values: points.map((p) => p.ratePerSec),
                      className: "text-primary",
                    },
                  ]}
                />
                <RedChart
                  title="Errors"
                  format={formatPercent}
                  series={[
                    {
                      label: "error rate",
                      values: points.map((p) => p.errorRate),
                      className: "text-error",
                    },
                  ]}
                />
                <RedChart
                  title="Duration"
                  format={formatMs}
                  series={[
                    { label: "p50", values: points.map((p) => p.p50Ms), className: "text-secondary" },
                    { label: "p95", values: points.map((p) => p.p95Ms), className: "text-warning" },
                    { label: "p99", values: points.map((p) => p.p99Ms), className: "text-error" },
                  ]}
                />
              </>
            )}
          </Card>
        </div>
      ) : (
        <ServiceSignals service={service} tab={tab} includeAux={includeAux} />
      )}
    </div>
  );
}

// Back to the inventory. A button rather than a <Link href="/services">,
// because the selection IS url state on this same screen: the App Router
// treats a link to the pathname it is already on as a no-op and leaves
// ?service= in place, while clearing the parameter is what "all services"
// actually means. `view` goes with it — a tab chosen for one service is not a
// preference to carry back to the list.
function BackLink() {
  const { setMany } = useURLState();
  return (
    <button
      type="button"
      onClick={() => setMany({ service: undefined, view: undefined })}
      className="inline-flex items-center gap-1 text-xs text-base-content/50 hover:text-primary"
    >
      <ArrowLeft className="h-3.5 w-3.5" aria-hidden /> All services
    </button>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "error";
}) {
  return (
    <Card className="flex flex-col gap-1 p-3">
      <span className="text-[11px] uppercase tracking-wide text-base-content/50">{label}</span>
      <span
        className={`font-mono text-lg ${tone === "error" ? "text-error" : "text-base-content"}`}
      >
        {value}
      </span>
    </Card>
  );
}
