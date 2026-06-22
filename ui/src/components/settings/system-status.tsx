"use client";

import { useSystemStatus } from "@/hooks/use-system-status";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatAgo, formatBytes } from "@/lib/format";
import type {
  ComponentHealth,
  DiskStats,
  HealthStatus,
  SignalStats,
} from "@/lib/api-types";

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

      {/* Storage usage — per signal */}
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Storage usage</CardTitle>
        </CardHeader>
        <table className="table-dense w-full text-sm">
          <thead>
            <tr className="border-y border-neutral text-left">
              <th>Signal</th>
              <th className="text-right">Size</th>
              <th className="text-right">Compression</th>
              <th className="text-right">Rows</th>
              <th className="text-right">Data since</th>
              <th className="text-right">Retention</th>
            </tr>
          </thead>
          <tbody>
            {data.signals.map((s) => (
              <SignalRow key={s.signal} s={s} />
            ))}
          </tbody>
        </table>
      </Card>

      {/* ClickHouse disk usage */}
      {data.disks.length > 0 && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>ClickHouse disk usage</CardTitle>
          </CardHeader>
          <div className="flex flex-col gap-3 border-t border-neutral p-4">
            {data.disks.map((d) => (
              <DiskBar key={d.name} d={d} />
            ))}
          </div>
        </Card>
      )}
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

function SignalRow({ s }: { s: SignalStats }) {
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium capitalize">{s.signal}</td>
      <td className="text-right font-mono">{formatBytes(s.compressedBytes)}</td>
      <td className="text-right font-mono text-base-content/70">
        {s.compression > 0 ? `${s.compression.toFixed(1)}x` : "—"}
      </td>
      <td className="text-right font-mono text-base-content/70">
        {s.rows.toLocaleString()}
      </td>
      <td className="text-right text-base-content/70">
        {s.oldest ? formatAgo(s.oldest) : "—"}
      </td>
      <td className="text-right font-mono text-base-content/70">
        {s.retentionDays}d
      </td>
    </tr>
  );
}

function DiskBar({ d }: { d: DiskStats }) {
  const used = Math.max(0, d.totalBytes - d.freeBytes);
  const pct = d.totalBytes > 0 ? (used / d.totalBytes) * 100 : 0;
  const barColor =
    pct >= 85 ? "bg-error" : pct >= 70 ? "bg-warning" : "bg-primary";

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium">{d.name}</span>
        <span className="font-mono text-base-content/60">
          {pct.toFixed(0)}% · {formatBytes(d.freeBytes)} free /{" "}
          {formatBytes(d.totalBytes)}
        </span>
      </div>
      <div className="h-2 w-full overflow-hidden rounded bg-base-300">
        <div
          className={`h-full ${barColor}`}
          style={{ width: `${Math.min(100, pct)}%` }}
        />
      </div>
    </div>
  );
}
