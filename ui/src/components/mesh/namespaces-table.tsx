"use client";

import { useMemo } from "react";
import { AlertTriangle } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatAgo } from "@/lib/format";
import type { MeshNamespace, MeshNamespacesResponse } from "@/lib/api-types";
import { MtlsLock } from "./posture";
import { SnapshotNotes, UnreadableState } from "./snapshot-notes";

type SortKey = "name" | "dataplaneMode" | "waypoint" | "mtlsMode" | "enrolled" | "services" | "errors";

const NAME: SortColumn<SortKey> = { key: "name", label: "Namespace" };
const MODE: SortColumn<SortKey> = { key: "dataplaneMode", label: "Dataplane" };
const WAYPOINT: SortColumn<SortKey> = { key: "waypoint", label: "Waypoint" };
const MTLS: SortColumn<SortKey> = { key: "mtlsMode", label: "mTLS" };
const ENROLLED: SortColumn<SortKey> = { key: "enrolled", label: "Enrolled", numeric: true };
const SERVICES: SortColumn<SortKey> = { key: "services", label: "Services", numeric: true };
const ISSUES: SortColumn<SortKey> = { key: "errors", label: "Issues", numeric: true };

// Namespaces as the cluster defines them.
//
// The rows come from LABELS, not from traffic, which is the entire reason this
// screen exists: a namespace enrolled in the mesh and silent is indistinguishable
// from an unenrolled one when all you have is telemetry — and silence is exactly
// what a broken enrolment produces.
export function NamespacesTable({ data }: { data: MeshNamespacesResponse }) {
  const sort = useColumnSort<SortKey>("name", true);
  const rows = useMemo(() => sort.sortRows(data.namespaces), [data.namespaces, sort]);

  if (data.state !== "ok") return <UnreadableState state={data.state} reason={data.reason} />;

  return (
    <div className="flex flex-col gap-2">
      <SnapshotNotes
        truncated={data.truncated}
        podsTruncated={data.podsTruncated}
        missingKinds={data.missingKinds}
        checksSkipped={data.checksSkipped}
      />
      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table data-testid="mesh-namespaces" className="table-dense w-full text-sm">
            <thead className="text-xs text-base-content/55">
              <tr className="border-b border-neutral text-left">
                <SortableTh col={NAME} sort={sort} />
                <SortableTh col={MODE} sort={sort} />
                <SortableTh col={WAYPOINT} sort={sort} />
                <SortableTh col={MTLS} sort={sort} />
                <SortableTh col={ENROLLED} sort={sort} iconFirst />
                <SortableTh col={SERVICES} sort={sort} iconFirst />
                <SortableTh col={ISSUES} sort={sort} iconFirst />
              </tr>
            </thead>
            <tbody>
              {rows.map((ns) => (
                <NamespaceRow key={ns.name} ns={ns} checksSkipped={data.checksSkipped} />
              ))}
            </tbody>
          </table>
        </div>
      </Card>
      {data.syncedAt && (
        <p className="text-xs text-base-content/40">
          Cluster read {formatAgo(data.syncedAt)}
        </p>
      )}
    </div>
  );
}

function NamespaceRow({ ns, checksSkipped }: { ns: MeshNamespace; checksSkipped?: string }) {
  // Enrolled and silent: the case that only exists because we read config. It
  // is not necessarily broken — a namespace can be idle — but it is the first
  // thing to look at when a mesh change did nothing.
  const enrolledAndSilent = !!ns.dataplaneMode && ns.services === 0;
  // Asked into the mesh, and some of its workloads are not in it: the row's
  // own version of the enrolment gap.
  const gap =
    !!ns.dataplaneMode && ns.workloads !== undefined && (ns.enrolled ?? 0) < ns.workloads;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium">{ns.name}</td>
      <td>
        {ns.dataplaneMode ? (
          <Badge tone={ns.dataplaneMode === "ambient" ? "primary" : "neutral"}>
            {ns.dataplaneMode}
          </Badge>
        ) : (
          <span className="text-base-content/40" title="Not enrolled in the mesh">
            out of mesh
          </span>
        )}
      </td>
      <td className="text-base-content/70">
        {ns.waypoint ? (
          <span title={ns.waypointNamespace ? `in ${ns.waypointNamespace}` : undefined}>
            {ns.waypoint}
          </span>
        ) : (
          "—"
        )}
      </td>
      <td>
        <MtlsLock own="namespace" mode={ns.mtlsMode} source={ns.mtlsSource} policy={ns.mtlsPolicy} />
      </td>
      {/* Absent when pods could not be read: a "0/0" would read as an empty
          namespace, and the title says what the dash actually means. */}
      <td
        className={`text-right tabular-nums ${gap ? "text-warning" : ""}`}
        title={
          ns.workloads === undefined
            ? checksSkipped ?? "Pods were not read"
            : gap
              ? "Enrolled in the mesh, and not every workload is in it"
              : undefined
        }
      >
        {ns.workloads === undefined ? (
          <span className="text-base-content/40">—</span>
        ) : (
          `${(ns.enrolled ?? 0).toLocaleString()}/${ns.workloads.toLocaleString()}`
        )}
      </td>
      <td
        className={`text-right tabular-nums ${enrolledAndSilent ? "text-warning" : ""}`}
        title={enrolledAndSilent ? "Enrolled in the mesh and sending no telemetry" : undefined}
      >
        {ns.services.toLocaleString()}
      </td>
      <td className="text-right tabular-nums">
        {ns.errors + ns.warnings === 0 ? (
          <span className="text-base-content/40">—</span>
        ) : (
          <span className={ns.errors > 0 ? "text-error" : "text-warning"}>
            <AlertTriangle className="mr-1 inline h-3 w-3" aria-hidden />
            {ns.errors + ns.warnings}
          </span>
        )}
      </td>
    </tr>
  );
}
