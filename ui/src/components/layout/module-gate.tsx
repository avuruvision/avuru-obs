"use client";

import { PowerOff } from "lucide-react";
import type { ReactNode } from "react";
import { useCapabilities } from "@/hooks/use-capabilities";
import type { ModuleName } from "@/lib/api-types";

// Helm value that turns each module back on, for the enablement hint.
const HELM_VALUE: Record<Exclude<ModuleName, "core">, string> = {
  logs: "modules.logs.enabled",
  "infra-metrics": "modules.infraMetrics.enabled",
  profiling: "modules.profiling.enabled",
  "error-tracking": "modules.errorTracking.enabled",
  "service-health": "modules.serviceHealth.enabled",
  alerting: "modules.alerting.enabled",
  mesh: "modules.mesh.enabled",
  "mesh-config": "modules.meshConfig.enabled",
  green: "modules.green.enabled",
  cost: "modules.cost.enabled",
  ai: "modules.ai.enabled",
  // No screen gates on mcp — it serves an agent, not a page — but the record
  // is exhaustive on purpose, so a new module has to be decided about here.
  mcp: "modules.mcp.enabled",
};

const LABEL: Record<Exclude<ModuleName, "core">, string> = {
  logs: "Logs",
  "infra-metrics": "Infrastructure metrics",
  profiling: "Continuous profiling",
  "error-tracking": "Error tracking",
  "service-health": "Service health",
  alerting: "Alerting",
  mesh: "Service mesh",
  "mesh-config": "Mesh configuration",
  green: "Energy & carbon",
  cost: "Cost & waste",
  ai: "AI observability",
  mcp: "MCP server",
};

// Renders `children` unless this install doesn't run `module`, in which case
// it explains how to turn it on. The sidebar already hides inactive modules;
// this catches direct navigation and old bookmarks, which would otherwise hit
// a 404 from the hub and surface as a generic error.
export function ModuleGate({
  module,
  children,
}: {
  module: Exclude<ModuleName, "core">;
  children: ReactNode;
}) {
  const { data } = useCapabilities();

  // Unknown (loading, or a hub without the endpoint) → render. Same reasoning
  // as useModuleEnabled: modules are all-on by default.
  if (!data || data.modules.includes(module)) return <>{children}</>;

  return (
    <div className="flex flex-1 items-center justify-center p-8">
      <div className="max-w-md text-center">
        <PowerOff
          className="mx-auto mb-4 h-10 w-10 text-base-content/25"
          aria-hidden
        />
        <h2 className="mb-2 text-lg font-semibold">
          The {LABEL[module]} module is off
        </h2>
        <p className="mb-4 text-sm text-base-content/60">
          This install doesn&apos;t run it, so there is nothing to show here.
          Turn it on to start collecting and storing this signal.
        </p>
        <code className="block rounded-lg bg-base-200 px-3 py-2 text-left text-xs">
          helm upgrade … --set {HELM_VALUE[module]}=true
        </code>
      </div>
    </div>
  );
}
