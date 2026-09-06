"use client";

import { useMemo } from "react";
import { AlertTriangle, Network } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatAgo } from "@/lib/format";
import type { MeshNamespace, MeshNamespacesResponse } from "@/lib/api-types";

type SortKey = "name" | "dataplaneMode" | "waypoint" | "mtlsMode" | "services" | "errors";

const NAME: SortColumn<SortKey> = { key: "name", label: "Namespace" };
const MODE: SortColumn<SortKey> = { key: "dataplaneMode", label: "Dataplane" };
const WAYPOINT: SortColumn<SortKey> = { key: "waypoint", label: "Waypoint" };
const MTLS: SortColumn<SortKey> = { key: "mtlsMode", label: "mTLS" };
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

  if (data.state !== "ok") {
    return (
      <EmptyState icon={Network} title={unreadableTitle(data.state)}>
        {data.reason ?? "The cluster's mesh configuration could not be read."}
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      {(data.missingKinds?.length || data.truncated) && (
        <p className="text-xs text-base-content/55">
          {data.truncated && "This cluster is larger than one snapshot; the list is cut short. "}
          {data.missingKinds?.length
            ? `Not readable here: ${data.missingKinds.join(", ")}.`
            : null}
        </p>
      )}
      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table data-testid="mesh-namespaces" className="table-dense w-full text-sm">
            <thead className="text-xs text-base-content/55">
              <tr className="border-b border-neutral text-left">
                <SortableTh col={NAME} sort={sort} />
                <SortableTh col={MODE} sort={sort} />
                <SortableTh col={WAYPOINT} sort={sort} />
                <SortableTh col={MTLS} sort={sort} />
                <SortableTh col={SERVICES} sort={sort} iconFirst />
                <SortableTh col={ISSUES} sort={sort} iconFirst />
              </tr>
            </thead>
            <tbody>
              {rows.map((ns) => (
                <NamespaceRow key={ns.name} ns={ns} />
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

function NamespaceRow({ ns }: { ns: MeshNamespace }) {
  // Enrolled and silent: the case that only exists because we read config. It
  // is not necessarily broken — a namespace can be idle — but it is the first
  // thing to look at when a mesh change did nothing.
  const enrolledAndSilent = !!ns.dataplaneMode && ns.services === 0;
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
      {/* Absent means no policy applies and the mesh default governs. The hub
          did not read that default, so the cell says so rather than guessing. */}
      <td className={ns.mtlsMode === "DISABLE" ? "text-warning" : "text-base-content/70"}>
        {ns.mtlsMode ?? <span className="text-base-content/40">default</span>}
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

function unreadableTitle(state: string): string {
  switch (state) {
    case "forbidden":
      return "Not allowed to read the cluster";
    case "no-crds":
      return "No mesh configuration in this cluster";
    default:
      return "Cluster configuration not read";
  }
}
