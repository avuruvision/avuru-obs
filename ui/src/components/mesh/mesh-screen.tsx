"use client";

import { useMemo } from "react";
import { AlertTriangle, CheckCircle2, Waypoints } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshControlPlane, useMeshProxies } from "@/hooks/use-mesh-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { Card } from "@/components/ui/card";
import { formatAgo, formatMs, formatPercent, formatRate } from "@/lib/format";
import type { MeshControlPlane, MeshProxy } from "@/lib/api-types";

// The mesh's own screen.
//
// Every other surface hides these workloads on purpose — their edges are hops,
// not dependencies, so a dependency graph that draws them is lying. That is the
// right call for the map and the wrong final word: on a cluster where the mesh
// IS the network, a proxy dropping requests or a control plane that has stopped
// pushing config is the outage.
export function MeshScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const query = get("q") ?? "";

  const proxies = useMeshProxies(time);
  const controlPlane = useMeshControlPlane(time);

  const list = useMemo(() => proxies.data?.proxies ?? [], [proxies.data]);
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? list.filter((p) => p.name.toLowerCase().includes(q)) : list;
  }, [list, query]);

  if (proxies.isLoading) return <CenteredSpinner />;

  return (
    <div className="flex flex-col gap-4">
      <ControlPlaneCard data={controlPlane.data} loading={controlPlane.isLoading} />

      {list.length === 0 ? (
        <EmptyState icon={Waypoints} title="No mesh workloads in this window">
          Nothing here is classified as transport — no sidecars, waypoints or
          gateways have sent telemetry. If your proxies are running but missing,
          the classification is correctable per install through the hub&apos;s
          topology config, without waiting for a release.
        </EmptyState>
      ) : (
        <Card className="overflow-hidden">
          <div className="flex items-center justify-between gap-3 border-b border-neutral px-4 py-3">
            <h2 className="text-sm font-medium">Proxies &amp; gateways</h2>
            <input
              type="search"
              aria-label="Filter proxies"
              placeholder="Filter…"
              value={query}
              onChange={(e) => setMany({ q: e.target.value || undefined })}
              className="w-48 rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs"
            />
          </div>
          <div className="overflow-x-auto">
            <table data-testid="mesh-proxies" className="w-full text-sm">
              <thead className="text-xs text-base-content/55">
                <tr className="border-b border-neutral">
                  <th className="px-4 py-2 text-left font-medium">Workload</th>
                  <th className="px-4 py-2 text-right font-medium">Rate</th>
                  <th className="px-4 py-2 text-right font-medium">Success</th>
                  <th className="px-4 py-2 text-right font-medium">p50</th>
                  <th className="px-4 py-2 text-right font-medium">p95</th>
                  <th className="px-4 py-2 text-right font-medium">Carried in</th>
                  <th className="px-4 py-2 text-right font-medium">Carried out</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((p) => (
                  <ProxyRow key={p.name} proxy={p} />
                ))}
              </tbody>
            </table>
          </div>
          {visible.length === 0 && (
            <p className="px-4 py-3 text-xs text-base-content/55">
              No proxy matches that filter.
            </p>
          )}
        </Card>
      )}
    </div>
  );
}

function ProxyRow({ proxy }: { proxy: MeshProxy }) {
  // Traffic in with none coming out is a proxy that stopped forwarding — a
  // failure its own error rate can miss entirely, which is why the two numbers
  // sit side by side rather than being summed into "throughput".
  const stalled = proxy.callsIn > 0 && proxy.callsOut === 0;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="px-4 py-2 font-medium">{proxy.name}</td>
      <td className="px-4 py-2 text-right tabular-nums">{formatRate(proxy.ratePerSec)}</td>
      <td
        className={`px-4 py-2 text-right tabular-nums ${proxy.errorRate > 0 ? "text-error" : ""}`}
      >
        {formatPercent(1 - proxy.errorRate)}
      </td>
      <td className="px-4 py-2 text-right tabular-nums">{formatMs(proxy.p50Ms)}</td>
      <td className="px-4 py-2 text-right tabular-nums">{formatMs(proxy.p95Ms)}</td>
      <td className="px-4 py-2 text-right tabular-nums">{proxy.callsIn.toLocaleString()}</td>
      <td
        className={`px-4 py-2 text-right tabular-nums ${stalled ? "text-warning" : ""}`}
        title={stalled ? "Traffic arriving, nothing forwarded" : undefined}
      >
        {proxy.callsOut.toLocaleString()}
      </td>
    </tr>
  );
}

// The control plane, or an honest statement that we are not watching it.
//
// Rendering zeros here would be the worst possible outcome: "0 rejected
// configs" from a control plane nobody scrapes reads as perfect health, and a
// fleet keeps serving its last good config long after istiod stops pushing.
function ControlPlaneCard({
  data,
  loading,
}: {
  data?: MeshControlPlane;
  loading: boolean;
}) {
  if (loading) return null;
  if (!data?.available) {
    return (
      <Card data-testid="mesh-control-plane" className="border-warning/40 p-4">
        <div className="flex items-start gap-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
          <div>
            <h2 className="text-sm font-medium">Control plane not observed</h2>
            <p className="mt-1 text-xs text-base-content/70">
              {data?.reason ??
                "No control-plane metrics in this window."}{" "}
              Until it is scraped, nothing here can tell you whether your proxies
              are still being programmed — a mesh keeps serving its last accepted
              configuration long after the control plane stops pushing.
            </p>
          </div>
        </div>
      </Card>
    );
  }

  // A rejected push means the control plane and the data plane disagree about
  // what the mesh should be doing. Nothing else in the product can surface it.
  const rejecting = (data.rejectedConfigs ?? 0) > 0;
  return (
    <Card data-testid="mesh-control-plane" className="p-4">
      <div className="flex items-center gap-2">
        {rejecting ? (
          <AlertTriangle className="h-4 w-4 text-warning" />
        ) : (
          <CheckCircle2 className="h-4 w-4 text-success" />
        )}
        <h2 className="text-sm font-medium">Control plane</h2>
        {data.lastSeen && (
          <span className="text-xs text-base-content/50">
            last seen {formatAgo(data.lastSeen)}
          </span>
        )}
      </div>
      <dl className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Connected proxies" value={(data.connectedProxies ?? 0).toLocaleString()} />
        <Stat label="Pushes" value={(data.pushes ?? 0).toLocaleString()} />
        <Stat
          label="Rejected configs"
          value={(data.rejectedConfigs ?? 0).toLocaleString()}
          tone={rejecting ? "warning" : undefined}
          hint={
            rejecting
              ? "Proxies refused configuration — the mesh is not running what you asked for"
              : undefined
          }
        />
        <Stat
          label="Convergence p95"
          value={data.convergenceP95Ms ? formatMs(data.convergenceP95Ms) : "—"}
        />
      </dl>
    </Card>
  );
}

function Stat({
  label,
  value,
  tone,
  hint,
}: {
  label: string;
  value: string;
  tone?: "warning";
  hint?: string;
}) {
  return (
    <div title={hint}>
      <dt className="text-xs text-base-content/55">{label}</dt>
      <dd
        className={`mt-0.5 text-lg tabular-nums ${tone === "warning" ? "text-warning" : ""}`}
      >
        {value}
      </dd>
    </div>
  );
}
