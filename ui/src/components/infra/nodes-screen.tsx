"use client";

import { Server } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useNodesData, usePodsData } from "@/hooks/use-infra-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { NodesTable } from "./nodes-table";
import { PodsPanel } from "./pods-panel";

// Node & pod health from kubeletstats (collected by the sensor DaemonSet).
// Selecting a node scopes the pods panel; selection lives in the URL.
export function NodesScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const node = get("node") ?? undefined;

  const nodes = useNodesData(time);
  const pods = usePodsData(time, node);

  if (nodes.isLoading) return <CenteredSpinner />;
  const nodeList = nodes.data?.nodes ?? [];

  if (!nodeList.length) {
    return (
      <EmptyState icon={Server} title="No node metrics yet">
        Node and pod health arrives with the sensor DaemonSet (kubeletstats) —
        it is enabled by default in the Helm chart. Data appears within a
        collection interval of installing.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <p className="text-xs text-base-content/55">
          {nodeList.length} nodes · click a node to scope the pods below.
        </p>
      </div>
      <NodesTable
        nodes={nodeList}
        selected={node}
        onSelect={(n) => setMany({ node: n })}
      />
      <h2 className="text-sm font-semibold text-base-content/80">
        Pods{node ? ` on ${node}` : ""} (busiest first)
      </h2>
      <PodsPanel pods={pods.data?.pods ?? []} node={node} />
    </div>
  );
}
