"use client";

import { useMemo } from "react";
import { AlertTriangle, ShieldAlert, ShieldCheck } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useURLState } from "@/hooks/use-url-state";
import { formatAgo, formatShare } from "@/lib/format";
import { Figure } from "./figure";
import { FindingCard } from "./config-browser";
import { postureLabel } from "./posture-badge";
import { SecurityTable } from "./security-table";
import type { MeshSecurityResponse, MeshWorkloadPosture } from "@/lib/api-types";

// What the cluster declared about mutual TLS, beside what the proxies saw.
//
// The namespace list says "STRICT" and the map says nothing, and a STRICT
// policy that is not applied to a workload looks, from configuration alone,
// exactly like one that is. This is the one screen that holds both halves —
// and it leads with whether the observed half was read at all, because a data
// plane nobody scrapes reports no plaintext, which reads as a fully encrypted
// mesh.
export function SecurityTab({
  data,
  loading,
}: {
  data?: MeshSecurityResponse;
  loading: boolean;
}) {
  const { get, setMany } = useURLState();
  const namespace = get("secns") ?? "";
  const posture = get("posture") ?? "";

  const rows = useMemo(() => data?.workloads ?? [], [data]);
  // Facet options come from the rows in scope, so the screen never offers a
  // choice that would match nothing.
  const namespaces = useMemo(() => [...new Set(rows.map((r) => r.namespace))].sort(), [rows]);
  const postures = useMemo(() => [...new Set(rows.map((r) => r.posture))].sort(), [rows]);
  const visible = useMemo(
    () =>
      rows.filter(
        (r) => (!namespace || r.namespace === namespace) && (!posture || r.posture === posture),
      ),
    [rows, namespace, posture],
  );
  const findings = useMemo(() => visible.flatMap((r) => r.findings ?? []), [visible]);

  if (loading) return <CenteredSpinner />;
  if (!data) return null;
  if (!data.available) {
    return (
      <div data-testid="mesh-security">
        <DataPlaneBanner data={data} />
      </div>
    );
  }

  return (
    <div data-testid="mesh-security" className="flex flex-col gap-4">
      <SummaryCard data={data} rows={rows} />
      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-neutral px-4 py-3">
          <h2 className="text-sm font-medium">Workloads</h2>
          <div className="flex flex-wrap items-center gap-2">
            {postures.length > 1 && (
              <Select
                ariaLabel="Filter by posture"
                className="w-44"
                value={posture}
                onChange={(v) => setMany({ posture: v || undefined })}
                options={[
                  { value: "", label: "All postures" },
                  ...postures.map((p) => ({ value: p, label: postureLabel(p) })),
                ]}
              />
            )}
            {namespaces.length > 1 && (
              <Select
                ariaLabel="Filter security by namespace"
                className="w-48"
                value={namespace}
                onChange={(v) => setMany({ secns: v || undefined })}
                options={[
                  { value: "", label: "All namespaces" },
                  ...namespaces.map((n) => ({ value: n, label: n })),
                ]}
              />
            )}
          </div>
        </div>
        {rows.length === 0 ? (
          <p className="px-4 py-3 text-xs text-base-content/55">
            The proxies answered and reported no workload in this window.
          </p>
        ) : (
          <SecurityTable rows={visible} declared={data.declared} />
        )}
        {rows.length > 0 && visible.length === 0 && (
          <p className="px-4 py-3 text-xs text-base-content/55">
            No workload matches those filters.
          </p>
        )}
        {/* The column is missing, not empty, and the caption says why: a row
            of "default" from a module that never read the cluster would claim
            a policy nobody looked up. */}
        {!data.declared && (
          <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/55">
            Declared mode not read — enable the mesh-config module to see what
            the cluster says.
          </p>
        )}
      </Card>
      {findings.length > 0 && (
        <section data-testid="mesh-security-findings" className="flex flex-col gap-2">
          <h2 className="text-sm font-medium">Needs attention</h2>
          {findings.map((f, i) => (
            <FindingCard key={`${f.code}-${i}`} finding={f} />
          ))}
        </section>
      )}
    </div>
  );
}

// Three silences, three fixes — the control-plane card's layout, because the
// reader has already learnt it there. The proxy names that did not answer are
// listed in full: "3 of 12" sends someone to look; the names tell them where.
function DataPlaneBanner({ data }: { data: MeshSecurityResponse }) {
  const heading =
    data.state === "unreachable"
      ? "Data plane not answering"
      : data.state === "unrecognised"
        ? "Data plane not recognised"
        : "Data plane not observed";
  const down = data.targets?.down ?? [];
  return (
    <Card className="border-warning/40 p-4">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
        <div className="min-w-0">
          <h2 className="text-sm font-medium">{heading}</h2>
          <p className="mt-1 text-xs text-base-content/70">
            {data.reason ?? "No data-plane metrics in this window."} Until the
            proxies are read, nothing here can say whether traffic crossed the
            mesh under mutual TLS — a data plane nobody scrapes reports no
            plaintext at all, which reads as a fully encrypted mesh.
          </p>
          {data.targets && (
            <p className="mt-2 text-xs text-base-content/50">
              {data.targets.up.toLocaleString()} of {data.targets.total.toLocaleString()} proxy
              targets answering
              {down.length > 0 && (
                <>
                  {" "}
                  · not answering: <span className="font-mono">{down.join(", ")}</span>
                </>
              )}
            </p>
          )}
        </div>
      </div>
    </Card>
  );
}

// The whole mesh in three numbers. The share is the hub's arithmetic — mutual
// TLS over classified units, unknown left out — summed across every reported
// workload, and absent when nothing was classified: 0/0 is not 0%.
function SummaryCard({ data, rows }: { data: MeshSecurityResponse; rows: MeshWorkloadPosture[] }) {
  let mtls = 0;
  let plaintext = 0;
  for (const r of rows) {
    mtls += r.observed?.mtls ?? 0;
    plaintext += r.observed?.plaintext ?? 0;
  }
  const share = mtls + plaintext > 0 ? mtls / (mtls + plaintext) : undefined;
  const attention = rows.filter((r) =>
    r.findings?.some((f) => f.severity === "error" || f.severity === "warning"),
  ).length;
  const clean = plaintext === 0 && attention === 0;
  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center gap-2">
        {clean ? (
          <ShieldCheck className="h-4 w-4 text-success" aria-hidden />
        ) : (
          <ShieldAlert className="h-4 w-4 text-warning" aria-hidden />
        )}
        <h2 className="text-sm font-medium">Data plane</h2>
        {data.targets && (
          <span className="text-xs text-base-content/50">
            {data.targets.up.toLocaleString()} of {data.targets.total.toLocaleString()} proxy
            targets answering
          </span>
        )}
        {data.lastSeen && (
          <span className="text-xs text-base-content/50">last seen {formatAgo(data.lastSeen)}</span>
        )}
      </div>
      <dl className="mt-3 grid grid-cols-3 gap-4">
        <Figure
          label="Observed mTLS"
          value={share === undefined ? "—" : formatShare(share)}
          tone={share !== undefined && share < 0.999 ? "warning" : undefined}
          hint={
            share === undefined
              ? "No proxy classified any traffic in this window"
              : "Share of requests and connections the proxies accepted over mutual TLS"
          }
        />
        <Figure
          label="Plaintext units"
          value={plaintext.toLocaleString()}
          tone={plaintext > 0 ? "warning" : undefined}
          hint="Requests and connections that reached a proxy in the clear"
        />
        <Figure
          label="Need attention"
          value={attention.toLocaleString()}
          tone={attention > 0 ? "warning" : undefined}
          hint="Workloads with a finding against their posture"
        />
      </dl>
    </Card>
  );
}
