"use client";

import { useMemo } from "react";
import { AlertTriangle, Boxes } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshWorkloads } from "@/hooks/use-mesh-data";
import { formatAgo, formatRate } from "@/lib/format";
import type { MeshWorkload } from "@/lib/api-types";
import { EnrolmentBadge, MtlsLock, ObservedMtls } from "./posture";
import { PostureBadge } from "./posture-badge";
import { SnapshotNotes, UnreadableState } from "./snapshot-notes";

type SortKey = "key" | "enrolment" | "waypoint" | "mtls" | "observed" | "rate" | "issues";
type Row = MeshWorkload & {
  key: string;
  enrolment: string;
  mtls: string;
  observed: number;
  rate: number;
  issues: number;
};

const WORKLOAD: SortColumn<SortKey> = { key: "key", label: "Workload" };
const ENROLMENT: SortColumn<SortKey> = { key: "enrolment", label: "Enrolment" };
const WAYPOINT: SortColumn<SortKey> = { key: "waypoint", label: "Waypoint" };
const DECLARED: SortColumn<SortKey> = { key: "mtls", label: "Declared mTLS" };
const OBSERVED: SortColumn<SortKey> = { key: "observed", label: "Observed mTLS", numeric: true };
const TRAFFIC: SortColumn<SortKey> = { key: "rate", label: "Traffic", numeric: true };
const ISSUES: SortColumn<SortKey> = { key: "issues", label: "Issues", numeric: true };

// The hub's own vocabulary for the mode filter. Fixed rather than derived from
// the rows: "declared-only" is a gap, not a value any row carries, and it is
// the choice this tab exists to offer.
const MODES = [
  { value: "", label: "All modes" },
  { value: "ambient", label: "Ambient", hint: "captured by the node agent" },
  { value: "sidecar", label: "Sidecar", hint: "a proxy injected in the pod" },
  { value: "none", label: "Out of mesh" },
  { value: "declared-only", label: "Declared, not enrolled", hint: "asked for, and never happened" },
];

// Every workload the cluster runs, in the mesh or not.
//
// The rows come from pods, not from traffic, which is why this tab exists: a
// workload asked into the mesh and never enrolled sends nothing of its own, and
// is the most common way an ambient mesh is misconfigured. Everywhere else in
// the product it is simply absent.
export function WorkloadsTable() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const namespace = get("wlns") ?? "";
  const mode = get("mode") ?? "";

  // The namespace facet is applied here, on rows already fetched, so it keeps
  // offering every namespace in scope; the mode goes to the hub, which owns
  // what "declared-only" means.
  const query = useMeshWorkloads(time, true, { mode: mode || undefined });
  const all = useMemo(() => query.data?.workloads ?? [], [query.data]);
  const namespaces = useMemo(
    () => [...new Set([...all.map((w) => w.namespace), ...(namespace ? [namespace] : [])])].sort(),
    [all, namespace],
  );

  // Worst first: the enrolment gap and the broken policies float to the top.
  const sort = useColumnSort<SortKey>("issues", false);
  const rows = useMemo(
    () =>
      sort.sortRows<Row>(
        all
          .filter((w) => !namespace || w.namespace === namespace)
          .map((w) => ({
            ...w,
            key: `${w.namespace}/${w.name}`,
            enrolment: enrolmentRank(w),
            mtls: w.declaredMtls?.mode ?? "",
            observed: w.observedMtls?.mtlsShare ?? -1,
            rate: w.ratePerSec ?? -1,
            issues: w.errors + w.warnings,
          })),
      ),
    [all, namespace, sort],
  );

  if (query.isLoading) return <CenteredSpinner />;
  const data = query.data;
  if (!data) return null;
  if (data.state !== "ok") return <UnreadableState state={data.state} reason={data.reason} />;

  return (
    <div className="flex flex-col gap-2">
      <SnapshotNotes
        truncated={data.truncated}
        podsTruncated={data.podsTruncated}
        missingKinds={data.missingKinds}
        checksSkipped={data.checksSkipped}
      />
      <div className="flex flex-wrap items-center gap-2">
        <Select
          ariaLabel="Filter by mode"
          className="w-56"
          value={mode}
          onChange={(v) => setMany({ mode: v || undefined })}
          options={MODES}
        />
        {namespaces.length > 1 && (
          <Select
            ariaLabel="Filter workloads by namespace"
            className="w-52"
            value={namespace}
            onChange={(v) => setMany({ wlns: v || undefined })}
            options={[
              { value: "", label: "All namespaces" },
              ...namespaces.map((n) => ({ value: n, label: n })),
            ]}
          />
        )}
      </div>
      {rows.length === 0 ? (
        <EmptyState icon={Boxes} title="No workloads listed">
          {data.checksSkipped
            ? "The hub could not read the cluster's pods, and a workload is a fact about pods."
            : "Nothing the snapshot holds matches these filters. A workload appears here once the cluster runs a pod for it, whether or not it ever sends a span."}
        </EmptyState>
      ) : (
        <Card className="overflow-hidden">
          <div className="overflow-x-auto">
            <table data-testid="mesh-workloads" className="table-dense w-full text-sm">
              <thead className="text-xs text-base-content/55">
                <tr className="border-b border-neutral text-left">
                  <SortableTh col={WORKLOAD} sort={sort} />
                  <SortableTh col={ENROLMENT} sort={sort} />
                  <SortableTh col={WAYPOINT} sort={sort} />
                  <SortableTh col={DECLARED} sort={sort} />
                  <SortableTh col={OBSERVED} sort={sort} iconFirst />
                  <SortableTh col={TRAFFIC} sort={sort} iconFirst />
                  <SortableTh col={ISSUES} sort={sort} iconFirst />
                </tr>
              </thead>
              <tbody>
                {rows.map((w) => (
                  <WorkloadRow key={w.key} w={w} onSelect={() => setMany({ wl: w.key })} />
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {data.syncedAt && (
        <p className="text-xs text-base-content/40">Cluster read {formatAgo(data.syncedAt)}</p>
      )}
    </div>
  );
}

function WorkloadRow({ w, onSelect }: { w: Row; onSelect: () => void }) {
  return (
    <tr
      onClick={onSelect}
      className="cursor-pointer border-b border-neutral/50 last:border-0 hover:bg-base-300/40"
    >
      <td>
        <div className="flex items-center gap-2">
          {/* A button, not a link: the page is a query-param state of this
              screen, and the row must stay reachable by keyboard. */}
          <button type="button" className="text-left font-medium hover:text-primary hover:underline">
            <span className="text-base-content/50">{w.namespace}/</span>
            {w.name}
          </button>
          <Badge className="text-[10px]">{w.kind}</Badge>
        </div>
      </td>
      <td>
        <EnrolmentBadge
          declaredMode={w.declaredMode}
          dataplaneMode={w.dataplaneMode}
          injected={w.injected}
          captured={w.captured}
        />
      </td>
      <td className="text-base-content/70">
        {w.waypoint ? (
          <span title={w.waypointNamespace ? `in ${w.waypointNamespace}` : undefined}>
            {w.waypoint}
            {w.waypointSource && (
              <span className="ml-1 text-[10px] text-base-content/45">
                via {w.waypointSource}
              </span>
            )}
          </span>
        ) : (
          "—"
        )}
      </td>
      <td>
        <MtlsLock
          own="workload"
          mode={w.declaredMtls?.mode}
          source={w.declaredMtls?.source}
          policy={w.declaredMtls?.policy}
        />
      </td>
      <td className="text-right">
        <ObservedMtls value={w.observedMtls} />
        {w.posture && w.posture !== "unknown" && w.posture !== "idle" && (
          <div className="mt-0.5">
            <PostureBadge posture={w.posture} />
          </div>
        )}
      </td>
      <td
        className={`text-right tabular-nums ${(w.errorRate ?? 0) > 0 ? "text-error" : ""}`}
        title={
          w.hasTraffic
            ? w.errorRate
              ? `${(w.errorRate * 100).toFixed(1)}% of requests failed`
              : undefined
            : "No telemetry from this workload in the window"
        }
      >
        {w.hasTraffic && w.ratePerSec !== undefined ? (
          formatRate(w.ratePerSec)
        ) : (
          <span className="text-base-content/40">—</span>
        )}
      </td>
      <td className="text-right tabular-nums">
        {w.issues === 0 ? (
          <span className="text-base-content/40">—</span>
        ) : (
          <span className={w.errors > 0 ? "text-error" : "text-warning"}>
            <AlertTriangle className="mr-1 inline h-3 w-3" aria-hidden />
            {w.issues}
          </span>
        )}
      </td>
    </tr>
  );
}

// Sort order for the enrolment column: the gap first, then out of mesh, then
// the two healthy answers — so a sort on it is a sort by how much attention
// the row needs.
function enrolmentRank(w: MeshWorkload): string {
  if (w.captured || w.injected || w.dataplaneMode) return "3-enrolled";
  if (w.declaredMode) return "1-declared-only";
  return "2-none";
}
