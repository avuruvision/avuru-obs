"use client";

import { Radio } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatAgo } from "@/lib/format";
import { useAgents } from "@/hooks/use-agents";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import type { AgentSignals } from "@/lib/api-types";

const SIGNALS: { key: keyof AgentSignals; label: string }[] = [
  { key: "traces", label: "Traces" },
  { key: "logs", label: "Logs" },
  { key: "metrics", label: "Metrics" },
  { key: "profiles", label: "Profiles" },
];

function SignalBadge({ label, seen }: { label: string; seen: string | null }) {
  return (
    <Badge tone={seen ? "success" : "neutral"}>
      {label}
      {seen ? ` · ${formatAgo(seen)}` : " · —"}
    </Badge>
  );
}

// Coroot-style agent status ("sensor: N nodes reporting"), derived from
// telemetry freshness — read-only. Runtime toggles arrive with the OpAMP
// control plane (v0.2); until then collection is controlled through Helm
// values (see the toggle matrix in deploy/helm/README.md).
export function CollectionSettings() {
  // The inventory is derived from sensor freshness in the metrics tables, so
  // it needs the infra-metrics module; without it the query would just 404.
  const infraMetrics = useModuleEnabled("infra-metrics");
  const { data, isLoading, isError } = useAgents(600, { enabled: infraMetrics });

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Sensors</CardTitle>
          {data && (
            <span className="text-xs text-base-content/50">
              {data.sensors.length} node{data.sensors.length === 1 ? "" : "s"} reporting in the
              last {Math.round(data.windowSeconds / 60)}m
            </span>
          )}
        </CardHeader>
        {!infraMetrics ? (
          <p className="border-t border-neutral p-4 text-sm text-base-content/60">
            The sensor inventory reads telemetry freshness from the
            infrastructure metrics module, which this install doesn’t run.
            Enable it with{" "}
            <code className="font-mono">modules.infraMetrics.enabled=true</code>.
          </p>
        ) : isLoading ? (
          <div className="border-t border-neutral p-6">
            <CenteredSpinner />
          </div>
        ) : isError || !data ? (
          <p className="border-t border-neutral p-4 text-sm text-error">
            Couldn’t reach the hub to read the agent inventory.
          </p>
        ) : data.sensors.length === 0 ? (
          <div className="border-t border-neutral">
            <EmptyState icon={Radio} title="No sensors reporting">
              No node has shipped telemetry in the window. Install the sensor
              DaemonSet (chart value <code className="font-mono">sensor.enabled</code>) or
              check the runbook if pods are unhealthy.
            </EmptyState>
          </div>
        ) : (
          <ul className="divide-y divide-neutral border-t border-neutral">
            {data.sensors.map((s) => (
              <li key={s.node} className="flex flex-wrap items-center gap-2 p-3">
                <span className="min-w-40 font-mono text-sm font-medium">{s.node}</span>
                <span className="text-xs text-base-content/50">
                  last seen {formatAgo(s.lastSeen)}
                </span>
                <span className="ml-auto flex flex-wrap gap-1.5">
                  {SIGNALS.map(({ key, label }) => (
                    <SignalBadge key={key} label={label} seen={s.signals[key]} />
                  ))}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Deactivating collection</CardTitle>
          <span className="text-xs text-base-content/50">applied via Helm — runtime switches land with OpAMP (v0.2)</span>
        </CardHeader>
        <div className="flex flex-col gap-2 border-t border-neutral p-4 text-xs text-base-content/70">
          <p>
            <span className="font-semibold">Per signal:</span>{" "}
            <code className="font-mono">sensor.obi.enabled</code> (traces),{" "}
            <code className="font-mono">sensor.agent.logs.enabled</code>,{" "}
            <code className="font-mono">sensor.agent.kubeletstats.enabled</code>,{" "}
            <code className="font-mono">sensor.profiler.enabled</code>
          </p>
          <p>
            <span className="font-semibold">Per namespace:</span>{" "}
            <code className="font-mono">sensor.collection.excludeNamespaces</code>
          </p>
          <p>
            <span className="font-semibold">Per pod:</span> label{" "}
            <code className="font-mono">avuru.obs/instrument: &quot;false&quot;</code>
          </p>
          <p>
            <span className="font-semibold">Per node (instant, no upgrade):</span>{" "}
            <code className="font-mono">kubectl label node &lt;n&gt; avuru.obs/collect=false</code>
          </p>
          <p className="text-base-content/45">
            Full matrix and examples: deploy/helm/README.md · probe issues:
            docs/runbooks/app-probe-failures.md
          </p>
        </div>
      </Card>
    </div>
  );
}
