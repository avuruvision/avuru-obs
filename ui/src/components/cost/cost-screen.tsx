"use client";

import { Wallet, TriangleAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useTimeRange } from "@/hooks/use-time-range";
import { useCostNodes, useCostWorkloads } from "@/hooks/use-cost-data";
import { formatBytes, formatPercent } from "@/lib/format";
import type { NodeCost, WorkloadCost } from "@/lib/api-types";

// Cost & waste: what each workload RESERVED against what it used, and what
// each node has against what has been claimed of it.
//
// The screen is ranked by idle capacity rather than by spend, and that is the
// point of it: the biggest workload in the cluster is not a finding, and the
// one reserving eight cores to use a tenth of one is. Money is a column when
// the install declared rates and simply absent when it did not — there is no
// pricing API behind this and inventing a currency would make every row a
// guess (design/2026-08-26-cost-and-waste.md).
export function CostScreen() {
  const { time } = useTimeRange();
  const workloads = useCostWorkloads(time);
  const nodes = useCostNodes(time);

  if (workloads.isLoading) return <CenteredSpinner />;
  if (workloads.isError) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read reserved capacity.
      </Card>
    );
  }

  const rows = workloads.data?.workloads ?? [];
  const priced = !!workloads.data?.priced;
  const currency = workloads.data?.currency ?? "";

  if (!rows.length) {
    return (
      <EmptyState icon={Wallet} title="Nothing reserved yet">
        This reads what Kubernetes says each container <em>requests</em> and
        compares it with what the kubelet says it uses. Both arrive from the
        sensor already running — switch the collection on with{" "}
        <code className="rounded bg-base-300 px-1">
          modules.cost.enabled=true
        </code>{" "}
        and give it a scrape interval or two.
      </EmptyState>
    );
  }

  const idleCores = rows.reduce((sum, r) => sum + r.idleCpuCores, 0);
  const idleBytes = rows.reduce((sum, r) => sum + r.idleMemBytes, 0);
  const idleMoney = priced
    ? rows.reduce((sum, r) => sum + (r.idleCostPerHour ?? 0), 0)
    : 0;
  const unbounded = rows.filter((r) => r.requestsNothing);

  return (
    <div className="flex flex-col gap-5">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Reserved and idle</CardTitle>
          {!priced && (
            <span className="text-xs text-base-content/50">
              no rates configured — set{" "}
              <span className="font-mono">cost.rates</span> to see money
            </span>
          )}
        </CardHeader>
        <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-3">
          <Stat label="Idle CPU" value={`${idleCores.toFixed(2)} cores`} />
          <Stat label="Idle memory" value={formatBytes(idleBytes)} />
          {priced ? (
            <Stat
              label="Idle spend"
              value={`${idleMoney.toFixed(2)} ${currency}/h`}
              testid="cost-idle-money"
            />
          ) : (
            <Stat label="Workloads" value={String(rows.length)} />
          )}
        </div>
        {/* Reserving nothing is not a small version of reserving little: the
            scheduler cannot place it deliberately and the kubelet evicts it
            first. It leads the page rather than sitting in a row. */}
        {unbounded.length > 0 && (
          <div
            className="flex items-start gap-2 border-t border-neutral bg-warning/10 p-2.5 text-xs"
            data-testid="cost-unbounded-warning"
          >
            <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" aria-hidden />
            <span>
              <strong>
                {unbounded.length} workload{unbounded.length === 1 ? "" : "s"} request
                {unbounded.length === 1 ? "s" : ""} nothing at all
              </strong>{" "}
              — {unbounded.map((r) => r.workload).join(", ")}. The scheduler
              cannot size a node for them and the kubelet evicts them first.
            </span>
          </div>
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Workloads</CardTitle>
          <span className="text-xs text-base-content/50">
            ranked by capacity reserved and not used
          </span>
        </CardHeader>
        <div className="overflow-x-auto">
          <table className="table-dense w-full text-sm" data-testid="cost-workloads">
            <thead>
              <tr className="border-y border-neutral text-left">
                <th>Workload</th>
                <th className="text-right">Reserved CPU</th>
                <th className="text-right">Peak / mean used</th>
                <th className="text-right">Idle CPU</th>
                <th className="text-right">Reserved memory</th>
                <th className="text-right">Idle memory</th>
                {priced && <th className="text-right">Idle {currency}/h</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <WorkloadRow key={`${r.namespace}/${r.workload}`} r={r} priced={priced} />
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      {!!nodes.data?.nodes.length && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Nodes</CardTitle>
            <span className="text-xs text-base-content/50">
              allocatable, claimed by requests, actually used
            </span>
          </CardHeader>
          <div className="overflow-x-auto">
            <table className="table-dense w-full text-sm" data-testid="cost-nodes">
              <thead>
                <tr className="border-y border-neutral text-left">
                  <th>Node</th>
                  <th className="text-right">CPU requested</th>
                  <th className="text-right">CPU used</th>
                  <th className="text-right">Memory requested</th>
                  <th className="text-right">Memory used</th>
                </tr>
              </thead>
              <tbody>
                {nodes.data.nodes.map((n) => (
                  <NodeRow key={n.node} n={n} />
                ))}
              </tbody>
            </table>
          </div>
          {/* A node can be full and idle at once, and only one of those is a
              capacity problem. Saying it once here beats a legend nobody
              reads. */}
          <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
            A node that is fully <em>requested</em> takes no more pods, however
            little it is <em>using</em> — that gap is the same waste the
            workload table ranks, seen from the machine you are paying for.
          </p>
        </Card>
      )}
    </div>
  );
}

function Stat({ label, value, testid }: { label: string; value: string; testid?: string }) {
  return (
    <div className="bg-base-200 p-3" data-testid={testid}>
      <p className="text-xs uppercase tracking-wider text-base-content/50">{label}</p>
      <p className="text-sm font-semibold">{value}</p>
    </div>
  );
}

function WorkloadRow({ r, priced }: { r: WorkloadCost; priced: boolean }) {
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td>
        <span className="font-medium">{r.workload}</span>
        <span className="ml-1.5 text-xs text-base-content/45">{r.namespace}</span>
        {r.requestsNothing && (
          <Badge tone="warning" title="No CPU or memory request declared">
            requests nothing
          </Badge>
        )}
      </td>
      <td className="text-right">
        {r.requestsNothing ? "—" : `${r.reservedCpuCores.toFixed(2)}`}
      </td>
      <td className="text-right text-base-content/60">
        {r.usedCpuCoresPeak.toFixed(2)} / {r.usedCpuCoresMean.toFixed(2)}
      </td>
      <td className="text-right">
        {r.requestsNothing ? "—" : r.idleCpuCores.toFixed(2)}
      </td>
      <td className="text-right">
        {r.requestsNothing ? "—" : formatBytes(r.reservedMemBytes)}
      </td>
      <td className="text-right">
        {r.requestsNothing ? "—" : formatBytes(r.idleMemBytes)}
      </td>
      {priced && (
        <td className="text-right">
          {r.idleCostPerHour === undefined ? "—" : r.idleCostPerHour.toFixed(3)}
        </td>
      )}
    </tr>
  );
}

function NodeRow({ n }: { n: NodeCost }) {
  const cpuReq = n.allocatableCpuCores > 0 ? n.requestedCpuCores / n.allocatableCpuCores : 0;
  const cpuUse = n.allocatableCpuCores > 0 ? n.usedCpuCores / n.allocatableCpuCores : 0;
  const memReq = n.allocatableMemBytes > 0 ? n.requestedMemBytes / n.allocatableMemBytes : 0;
  const memUse = n.allocatableMemBytes > 0 ? n.usedMemBytes / n.allocatableMemBytes : 0;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium">{n.node}</td>
      <td className="text-right">{formatPercent(cpuReq)}</td>
      <td className="text-right text-base-content/60">{formatPercent(cpuUse)}</td>
      <td className="text-right">{formatPercent(memReq)}</td>
      <td className="text-right text-base-content/60">{formatPercent(memUse)}</td>
    </tr>
  );
}
