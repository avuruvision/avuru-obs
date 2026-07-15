"use client";

import { Info } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { useProject } from "@/lib/project-context";
import { useProjects } from "@/hooks/use-projects";
import { useSystemStatus } from "@/hooks/use-system-status";

// Coroot-style General settings: the active project, where it is defined,
// and this install's retention. Project CRUD, API keys, users/SSO arrive
// with v0.2 auth — until then projects are deployment configuration.
export function GeneralTab() {
  const { project } = useProject();
  const { data: projects } = useProjects();
  const { data: status } = useSystemStatus();

  const source = projects?.projects.find((p) => p.id === project)?.source;
  const sourceLabel =
    source === "data" ? "discovered from data" : source === "config" ? "config-defined" : "built-in";

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Project</CardTitle>
          {status && (
            <span className="text-xs text-base-content/50">hub {status.version}</span>
          )}
        </CardHeader>
        <div className="flex flex-col gap-3 border-t border-neutral p-4">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm font-semibold">{project}</span>
            <Badge tone="neutral">{sourceLabel}</Badge>
          </div>
          <p className="flex items-start gap-2 rounded-lg border border-info/40 bg-info/10 p-3 text-xs text-base-content/80">
            <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-info" aria-hidden />
            Projects are defined through deployment configuration and cannot be
            modified here. Declare them with the chart&apos;s{" "}
            <code className="font-mono">projects</code> value and tag each
            environment&apos;s telemetry with{" "}
            <code className="font-mono">gateway.tenant</code>; any tenant seen in
            the data appears automatically.
          </p>
        </div>
      </Card>

      {status && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Retention</CardTitle>
            <span className="text-xs text-base-content/50">
              per-signal TTL, instance-wide
            </span>
          </CardHeader>
          <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
            {status.signals.map((s) => (
              <div key={s.signal} className="bg-base-200 p-3">
                <p className="text-xs uppercase tracking-wider text-base-content/50">
                  {s.signal}
                </p>
                <p className="text-sm font-semibold">{s.retentionDays} days</p>
              </div>
            ))}
          </div>
        </Card>
      )}

      <p className="text-xs text-base-content/40">
        Roadmap: per-project API keys, users &amp; SSO, and project management
        land with authentication in v0.2.
      </p>
    </div>
  );
}
