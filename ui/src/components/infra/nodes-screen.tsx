"use client";

import { useMemo } from "react";
import { Search, Server } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useNodesData, usePodsData } from "@/hooks/use-infra-data";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { Select } from "@/components/ui/select";
import { Card } from "@/components/ui/card";
import { NodesTable } from "./nodes-table";
import { PodsPanel } from "./pods-panel";

const ALL_NAMESPACES = "__all__";

// Node & pod health from kubeletstats (collected by the sensor DaemonSet).
// Selecting a node scopes the pods panel; selection and both filters live in
// the URL, so a filtered view is shareable like every other screen here.
//
// Filtering is live as you type, unlike the Logs screen's Enter-to-search:
// there the query goes to the hub, here the window's rows are already in the
// browser and re-filtering costs nothing.
export function NodesScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const node = get("node") ?? undefined;
  const nodeQuery = get("nodeq") ?? "";
  const podQuery = get("podq") ?? "";
  const namespace = get("ns") ?? undefined;

  const nodes = useNodesData(time);
  const pods = usePodsData(time, node);

  const nodeList = useMemo(() => nodes.data?.nodes ?? [], [nodes.data]);
  const podList = useMemo(() => pods.data?.pods ?? [], [pods.data]);

  const visibleNodes = useMemo(() => {
    const q = nodeQuery.trim().toLowerCase();
    return q ? nodeList.filter((n) => n.name.toLowerCase().includes(q)) : nodeList;
  }, [nodeList, nodeQuery]);

  // Namespaces come from the pods currently in scope, so selecting a node
  // narrows the facet to what that node actually runs rather than offering
  // choices that would match nothing.
  const namespaces = useMemo(
    () => [...new Set(podList.map((p) => p.namespace))].sort(),
    [podList],
  );

  const visiblePods = useMemo(() => {
    const q = podQuery.trim().toLowerCase();
    return podList.filter((p) => {
      if (namespace && p.namespace !== namespace) return false;
      if (!q) return true;
      return (
        p.name.toLowerCase().includes(q) ||
        p.namespace.toLowerCase().includes(q) ||
        (p.workload ?? "").toLowerCase().includes(q)
      );
    });
  }, [podList, podQuery, namespace]);

  if (nodes.isLoading) return <CenteredSpinner />;

  // Nothing collected at all — a setup problem, and the only case that should
  // explain how to get data. A filter matching nothing is handled below.
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
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-base-content/55">
          {visibleNodes.length === nodeList.length
            ? `${nodeList.length} nodes`
            : `${visibleNodes.length} of ${nodeList.length} nodes`}{" "}
          · click a node to scope the pods below.
        </p>
        <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
          <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
          <input
            type="search"
            value={nodeQuery}
            placeholder="Filter nodes…"
            aria-label="Filter nodes by name"
            onChange={(e) => setMany({ nodeq: e.target.value || undefined })}
            className="h-9 w-48 bg-transparent text-sm outline-none placeholder:text-base-content/40"
            data-testid="node-filter"
          />
        </div>
      </div>

      {visibleNodes.length ? (
        <NodesTable
          nodes={visibleNodes}
          selected={node}
          onSelect={(n) => setMany({ node: n })}
        />
      ) : (
        <Card className="p-6 text-center text-sm text-base-content/60">
          No nodes match “{nodeQuery}”.
        </Card>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-base-content/80">
          Pods{node ? ` on ${node}` : ""}
          {visiblePods.length !== podList.length && (
            <span className="ml-1 font-normal text-base-content/50">
              — {visiblePods.length} of {podList.length}
            </span>
          )}
        </h2>
        <div className="flex flex-wrap items-center gap-2">
          {namespaces.length > 1 && (
            <Select
              value={namespace ?? ALL_NAMESPACES}
              ariaLabel="Filter pods by namespace"
              className="w-56"
              onChange={(v) => setMany({ ns: v === ALL_NAMESPACES ? undefined : v })}
              options={[
                { value: ALL_NAMESPACES, label: "All namespaces" },
                ...namespaces.map((ns) => ({ value: ns, label: ns })),
              ]}
            />
          )}
          <div className="flex items-center gap-1.5 rounded-lg border border-neutral bg-base-200 px-2">
            <Search className="h-3.5 w-3.5 text-base-content/50" aria-hidden />
            <input
              type="search"
              value={podQuery}
              placeholder="Filter pods…"
              aria-label="Filter pods by name, namespace or workload"
              onChange={(e) => setMany({ podq: e.target.value || undefined })}
              className="h-9 w-56 bg-transparent text-sm outline-none placeholder:text-base-content/40"
              data-testid="pod-filter"
            />
          </div>
        </div>
      </div>

      <PodsPanel
        pods={visiblePods}
        node={node}
        filtered={visiblePods.length !== podList.length || Boolean(podQuery || namespace)}
      />
    </div>
  );
}
