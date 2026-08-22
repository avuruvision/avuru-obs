"use client";

import { AlertTriangle } from "lucide-react";
import { useSystemStatus } from "@/hooks/use-system-status";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatAgo, formatBytes } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import type {
  DiskStats,
  ProjectUsage,
  SignalStats,
  StorageConnection,
} from "@/lib/api-types";

// Where the telemetry lives and how much of it there is. The connection is
// shown read-only on purpose rather than as a missing feature: ClickHouse is
// the store, so it cannot hold its own connection string — changing it is a
// chart value and a restart, and a form here would be a lie.
export function StorageTab() {
  const { data, isLoading, isError } = useSystemStatus();

  if (isLoading) return <CenteredSpinner />;
  if (isError || !data) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read storage details.
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {data.connection && <ConnectionCard c={data.connection} />}

      {data.project && <ProjectUsageCard p={data.project} />}

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Storage usage</CardTitle>
        </CardHeader>
        <table className="table-dense w-full text-sm" data-testid="storage-usage">
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
        <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
          Size is on disk (compressed). Retention is set with{" "}
          <code className="rounded bg-base-300 px-1">--set retention.traces=…</code>{" "}
          and applied to the tables when the migration runs. A single project
          can keep less — see Settings → General.
        </p>
      </Card>

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

function ConnectionCard({ c }: { c: StorageConnection }) {
  const rows: [string, string][] = [
    ["Address", c.address],
    ["Database", c.database],
    ["Protocol", c.protocol],
    ...(c.username ? ([["User", c.username]] as [string, string][]) : []),
  ];
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Connection</CardTitle>
      </CardHeader>
      <dl
        className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4"
        data-testid="storage-connection"
      >
        {rows.map(([label, value]) => (
          <div key={label} className="flex flex-col gap-1 bg-base-200 p-4">
            <dt className="text-xs text-base-content/50">{label}</dt>
            <dd className="truncate font-mono text-sm" title={value}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
      <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
        Read-only: ClickHouse is where everything is stored, so it can’t hold its
        own connection details. Change them with{" "}
        <code className="rounded bg-base-300 px-1">
          --set clickhouse.address=…
        </code>{" "}
        and restart the hub.
      </p>
    </Card>
  );
}

// ProjectUsageCard answers "what does THIS project hold?" — the question the
// instance-wide table below cannot answer on a shared install, and the one to
// settle before changing a project's retention. Sizes are estimates by
// construction: ClickHouse parts hold every tenant's rows together, so a
// project's share can only be apportioned by row count. The card says so
// rather than printing an exact-looking number.
function ProjectUsageCard({ p }: { p: ProjectUsage }) {
  const aggregate = p.tenants.length > 1;
  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>This project — {p.id}</CardTitle>
        {aggregate && <Badge tone="primary">aggregate of {p.tenants.length}</Badge>}
      </CardHeader>
      <table className="table-dense w-full text-sm" data-testid="project-usage">
        <thead>
          <tr className="border-y border-neutral text-left">
            <th>Signal</th>
            <th className="text-right">Size (est.)</th>
            <th className="text-right">Rows</th>
            <th className="text-right">Ingest</th>
            <th className="text-right">Data since</th>
            <th className="text-right">Keeps</th>
          </tr>
        </thead>
        <tbody>
          {p.signals.map((s) => (
            <tr key={s.signal} className="border-b border-neutral/50 last:border-0">
              <td className="font-medium capitalize">{s.signal}</td>
              <td className="text-right font-mono">{formatBytes(s.estimatedBytes)}</td>
              <td className="text-right font-mono text-base-content/70">
                {s.rows.toLocaleString()}
              </td>
              <td className="text-right font-mono text-base-content/70">
                {s.rowsPerMinute > 0 ? `${s.rowsPerMinute.toFixed(1)}/min` : "—"}
              </td>
              <td className="text-right text-base-content/70">
                {s.oldest ? formatAgo(s.oldest) : "—"}
              </td>
              <td className="text-right font-mono text-base-content/70">
                {p.retentionVaries ? (
                  <span title="Members keep different windows — open each member project">
                    varies
                  </span>
                ) : (
                  <>
                    {s.retentionDays}d{" "}
                    <span className="text-base-content/45">
                      {s.inherited ? "(install)" : "(own)"}
                    </span>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
        Size is an estimate: parts hold every project&apos;s rows together, so a
        project&apos;s share is apportioned by row count. Rows, ingest rate and
        &ldquo;data since&rdquo; are exact.
        {aggregate && <> Union of {p.tenants.join(", ")}.</>}
      </p>
    </Card>
  );
}

function SignalRow({ s }: { s: SignalStats }) {
  // The configured value and the one ClickHouse enforces are different facts.
  // They diverge when retention is changed after the tables exist and the
  // migration has not re-applied the TTL — until it does, the configured
  // number is a wish, so say which is which rather than showing one of them.
  const drifted = s.ttlDays > 0 && s.ttlDays !== s.retentionDays;
  const notApplied = s.ttlDays === 0;
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
        {drifted || notApplied ? (
          <span
            className="inline-flex items-center gap-1 text-warning"
            data-testid={`retention-drift-${s.signal}`}
            title={
              notApplied
                ? "No TTL on these tables yet — data is kept until the migration applies retention"
                : `Configured ${s.retentionDays}d, but the tables are enforcing ${s.ttlDays}d — re-run the migration to apply it`
            }
          >
            <AlertTriangle className="h-3 w-3" />
            {s.retentionDays}d → {notApplied ? "none" : `${s.ttlDays}d`}
          </span>
        ) : (
          `${s.retentionDays}d`
        )}
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
