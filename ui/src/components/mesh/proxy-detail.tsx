"use client";

import Link from "next/link";
import { useMemo } from "react";
import { ArrowLeft, ExternalLink, Waypoints } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { RedChart } from "@/components/metrics/red-chart";
import { useTimeRange } from "@/hooks/use-time-range";
import { useRedData } from "@/hooks/use-red-data";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useMeshWorkloadRequests } from "@/hooks/use-mesh-data";
import { formatBytes, formatMs, formatPercent, formatRate, formatShare } from "@/lib/format";
import { roleLabel } from "./mesh-roles";
import { CarriedTable } from "./carried-table";
import { Figure } from "./figure";
import { RequestBreakdown } from "./request-breakdown";
import type { MeshProxy } from "@/lib/api-types";

// The workload the mesh knows this proxy as. The traced service name carries
// the namespace as a suffix ("global-waypoint.istio-waypoint"); the data-plane
// series are keyed by namespace and bare name. Mirrors the hub's workloadKey,
// so the two sides ask about the same thing.
function workloadName(proxy: MeshProxy): string {
  const ns = proxy.namespace;
  if (!ns) return proxy.name;
  const suffix = `.${ns}`;
  return proxy.name.endsWith(suffix) && proxy.name.length > suffix.length
    ? proxy.name.slice(0, -suffix.length)
    : proxy.name;
}

// One proxy, and the question the table cannot answer: what is it carrying, and
// who loses it when this fails.
//
// Composed from the map read rather than a per-proxy endpoint. The dependencies
// this shows are the ones hop collapse recovered, and that reconstruction lives
// in the map response — a second query would eventually disagree with the map
// about what depends on what, and the two screens would tell different stories
// about the same estate.
export function ProxyDetail({
  proxy,
  onBack,
}: {
  proxy: MeshProxy;
  onBack: () => void;
}) {
  const { time } = useTimeRange();
  const map = useServiceMapData(time);
  const red = useRedData(time, proxy.name);
  // Keyed by namespace, so a proxy whose namespace could not be resolved asks
  // nothing rather than asking about a workload that does not exist.
  const requests = useMeshWorkloadRequests(
    time,
    proxy.namespace ?? "",
    workloadName(proxy),
    !!proxy.namespace,
  );

  // Every dependency the hub recovered ACROSS this proxy. viaTransport is
  // stamped by the collapse walk, so this is the proxy's real workload: the
  // app-to-app calls that only happen because it forwarded them.
  const carried = useMemo(
    () => (map.data?.edges ?? []).filter((e) => e.viaTransport?.includes(proxy.name)),
    [map.data, proxy.name],
  );

  const points = red.data?.series.find((s) => s.service === proxy.name)?.points ?? [];
  const plaintext = proxy.plaintextUnits ?? 0;
  const pending = proxy.pendingWorkloads ?? 0;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1 text-xs text-base-content/60 hover:text-base-content"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden />
          All proxies
        </button>
        <h1 className="font-mono text-lg">{proxy.name}</h1>
        {proxy.role && (
          <Badge tone={proxy.role === "control-plane" ? "primary" : "neutral"}>
            {roleLabel(proxy.role)}
          </Badge>
        )}
        {proxy.namespace && (
          <span className="text-xs text-base-content/60">{proxy.namespace}</span>
        )}
        {/* Traces, logs and errors for a proxy are the same screens every other
            workload uses. Linking beats reimplementing three tabs that would
            drift from the originals. */}
        <Link
          href={`/services?service=${encodeURIComponent(proxy.name)}`}
          className="ml-auto inline-flex items-center gap-1 text-xs text-primary hover:underline"
        >
          Traces, logs &amp; errors
          <ExternalLink className="h-3 w-3" aria-hidden />
        </Link>
      </div>

      <Card className="p-4">
        <dl data-testid="mesh-proxy-figures" className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Figure label="Rate" value={formatRate(proxy.ratePerSec)} />
          <Figure
            label="Success"
            value={formatPercent(1 - proxy.errorRate)}
            tone={proxy.errorRate > 0 ? "error" : undefined}
          />
          <Figure label="p50" value={formatMs(proxy.p50Ms)} />
          <Figure label="p95" value={formatMs(proxy.p95Ms)} />
          <Figure label="Calls in" value={proxy.callsIn.toLocaleString()} />
          <Figure
            label="Calls out"
            value={proxy.callsOut.toLocaleString()}
            // The failure the error rate misses entirely.
            tone={proxy.callsIn > 0 && proxy.callsOut === 0 ? "warning" : undefined}
            hint={
              proxy.callsIn > 0 && proxy.callsOut === 0
                ? "Traffic arriving, nothing forwarded"
                : undefined
            }
          />
          {/* Absent, not zero: an install without the flow metrics measured no
              bytes, which is not the same as a proxy that moved none. */}
          {proxy.bytesIn !== undefined && (
            <Figure label="Bytes in" value={formatBytes(proxy.bytesIn)} />
          )}
          {proxy.bytesOut !== undefined && (
            <Figure label="Bytes out" value={formatBytes(proxy.bytesOut)} />
          )}
          {proxy.rttMs !== undefined && (
            <Figure
              label="Link p95"
              value={formatMs(proxy.rttMs)}
              tone={(proxy.failedConnections ?? 0) > 0 ? "warning" : undefined}
              hint={
                (proxy.failedConnections ?? 0) > 0
                  ? `${(proxy.failedConnections ?? 0).toLocaleString()} failed connections on this proxy's links`
                  : undefined
              }
            />
          )}
          {/* What the proxy's own counters said about traffic addressed to it.
              Same rule as the bytes: a proxy the scrape did not report gets no
              figure, because "not measured" must not read as "all encrypted". */}
          {proxy.mtlsShare !== undefined && (
            <Figure
              label="mTLS"
              value={formatShare(proxy.mtlsShare)}
              tone={plaintext > 0 ? "warning" : undefined}
              hint={
                plaintext > 0
                  ? `${plaintext.toLocaleString()} requests or connections reached this proxy in the clear`
                  : "Every request and connection this proxy accepted came over mutual TLS"
              }
            />
          )}
          {/* ztunnel only: the node proxies' own account of what they carry.
              A waiting count is the finding — a workload told about and not
              yet wired is one whose traffic is crossing the node unmeshed. */}
          {proxy.role === "ztunnel" && proxy.activeWorkloads !== undefined && (
            <Figure
              label="Workloads carried"
              value={proxy.activeWorkloads.toLocaleString()}
              tone={pending > 0 ? "warning" : undefined}
              note={pending > 0 ? `${pending.toLocaleString()} waiting` : undefined}
              hint={
                pending > 0
                  ? "Workloads ztunnel has been told about and has not yet wired"
                  : undefined
              }
            />
          )}
          {proxy.role === "ztunnel" && proxy.xdsTerminations !== undefined && (
            <Figure
              label="XDS drops"
              value={proxy.xdsTerminations.toLocaleString()}
              tone={proxy.xdsTerminations > 0 ? "warning" : undefined}
              hint="Times the control-plane stream to ztunnel was cut"
            />
          )}
        </dl>
      </Card>

      {points.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-3">
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
              { label: "p95", values: points.map((p) => p.p95Ms), className: "text-warning" },
              { label: "p50", values: points.map((p) => p.p50Ms), className: "text-base-content/50" },
            ]}
          />
        </div>
      )}

      <RequestBreakdown data={requests.data} loading={requests.isLoading} />

      <section className="flex flex-col gap-2">
        <div>
          <h2 className="text-sm font-medium">Dependencies carried</h2>
          <p className="mt-0.5 text-xs text-base-content/55">
            Calls between applications that only happen because this proxy
            forwards them. When it fails, these are what breaks.
          </p>
        </div>
        {map.isLoading ? (
          <CenteredSpinner />
        ) : carried.length === 0 ? (
          <EmptyState icon={Waypoints} title="No dependencies recovered across this proxy">
            Either nothing routed through it in this window, or the calls it
            carries never span two applications — a gateway fronting a single
            service has no dependency to recover.
          </EmptyState>
        ) : (
          <CarriedTable edges={carried} />
        )}
      </section>
    </div>
  );
}
