"use client";

import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { Card } from "@/components/ui/card";
import { formatAgo, formatMs } from "@/lib/format";
import type { MeshControlPlane } from "@/lib/api-types";

// The control plane, or an honest statement that we are not watching it.
//
// Rendering zeros here would be the worst possible outcome: "0 rejected
// configs" from a control plane nobody scrapes reads as perfect health, and a
// fleet keeps serving its last good config long after istiod stops pushing.
export function ControlPlaneCard({
  data,
  loading,
}: {
  data?: MeshControlPlane;
  loading: boolean;
}) {
  if (loading) return null;
  if (!data?.available) {
    // Three silences, three fixes. "Not observed" used to cover all of them,
    // which sent an operator to check a scrape that was working perfectly —
    // and told the one running a different mesh nothing at all.
    const heading =
      data?.state === "unreachable"
        ? "Control plane not answering"
        : data?.state === "unrecognised"
          ? "Control plane not recognised"
          : "Control plane not observed";
    return (
      <Card data-testid="mesh-control-plane" className="border-warning/40 p-4">
        <div className="flex items-start gap-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
          <div>
            <h2 className="text-sm font-medium">{heading}</h2>
            <p className="mt-1 text-xs text-base-content/70">
              {data?.reason ?? "No control-plane metrics in this window."}{" "}
              Until it is read, nothing here can tell you whether your proxies
              are still being programmed — a mesh keeps serving its last accepted
              configuration long after the control plane stops pushing.
            </p>
            {/* The proxy table above is unaffected, and saying so stops this
                card reading as "the whole screen is broken". */}
            {data?.state === "unrecognised" && (
              <p className="mt-2 text-xs text-base-content/50">
                The proxies above are still measured — they come from your own
                traces, not from the control plane.
              </p>
            )}
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
        <h2 className="text-sm font-medium">
          Control plane
          {data.kind && (
            <span className="ml-1.5 font-normal text-base-content/50">{data.kind}</span>
          )}
        </h2>
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
          hint="Time for a config change to reach the proxies, including their acknowledgement"
        />
        {/* Optional series, from a widened keep-list. Absent means this install
            does not collect them — not that they are zero. Rendering a 0 write
            timeout from a scrape that never asked for it would be exactly the
            reassuring lie this card exists to prevent. */}
        {data.pushP95Ms !== undefined && (
          <Stat
            label="Push p95"
            value={formatMs(data.pushP95Ms)}
            hint="How long istiod itself takes to send a config. Convergence minus this is the proxies."
          />
        )}
        {data.writeTimeouts !== undefined && (
          <Stat
            label="Write timeouts"
            value={data.writeTimeouts.toLocaleString()}
            tone={data.writeTimeouts > 0 ? "warning" : undefined}
            hint={
              data.writeTimeouts > 0
                ? "Pushes that never landed — a proxy too slow or too gone to receive its configuration"
                : undefined
            }
          />
        )}
        {data.configEvents !== undefined && (
          <Stat
            label="Config events"
            value={data.configEvents.toLocaleString()}
            hint="Kubernetes config churn istiod is reacting to"
          />
        )}
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
