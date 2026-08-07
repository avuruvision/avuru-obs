"use client";

import { useSystemStatus } from "@/hooks/use-system-status";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import type { ComponentHealth, HealthStatus } from "@/lib/api-types";

type Tone = "success" | "error" | "warning" | "neutral";

function tone(status: HealthStatus | string): Tone {
  switch (status) {
    case "healthy":
      return "success";
    case "down":
      return "error";
    case "degraded":
    case "idle":
      return "warning";
    default:
      return "neutral";
  }
}

export function SystemStatus() {
  const { data, isLoading, isError } = useSystemStatus();

  if (isLoading) return <CenteredSpinner />;
  if (isError || !data) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read system status.
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Components — overall + per-component health */}
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Components</CardTitle>
          <div className="flex items-center gap-2">
            <span className="text-xs text-base-content/50">
              hub {data.version}
            </span>
            <Badge tone={tone(data.overall)}>
              {data.overall.toUpperCase()}
            </Badge>
          </div>
        </CardHeader>
        <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-3">
          {data.components.map((c) => (
            <ComponentTile key={c.name} c={c} />
          ))}
        </div>
      </Card>

      <p className="text-xs text-base-content/45">
        Storage usage, retention and the ClickHouse connection moved to{" "}
        <span className="font-mono">Settings → Storage</span>: this tab answers
        “is it healthy right now”, that one answers “where is my data and how
        much of it is there”.
      </p>
    </div>
  );
}

function ComponentTile({ c }: { c: ComponentHealth }) {
  return (
    <div className="flex flex-col gap-1 bg-base-200 p-4">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">{c.name}</span>
        <Badge tone={tone(c.status)}>{c.status}</Badge>
      </div>
      {c.detail && (
        <span className="text-xs text-base-content/50">{c.detail}</span>
      )}
    </div>
  );
}

