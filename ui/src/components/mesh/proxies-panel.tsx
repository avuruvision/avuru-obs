"use client";

import { useMemo } from "react";
import { Waypoints } from "lucide-react";
import { useURLState } from "@/hooks/use-url-state";
import { EmptyState } from "@/components/ui/empty-state";
import { Card } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import type { MeshProxy } from "@/lib/api-types";
import { ProxiesTable } from "./proxies-table";
import { namespacesPresent, roleLabel, rolesPresent } from "./mesh-roles";

// The Proxies tab: the table, its facets and its search. Moved out of the
// screen whole, so the screen is only the routing between tabs.
export function ProxiesPanel({ proxies: list }: { proxies: MeshProxy[] }) {
  const { get, setMany } = useURLState();
  const query = get("q") ?? "";
  const namespace = get("ns") ?? "";
  const role = get("role") ?? "";

  // Facet options come from the rows in scope, so the screen never offers a
  // choice that would match nothing.
  const namespaces = useMemo(() => namespacesPresent(list), [list]);
  const roles = useMemo(() => rolesPresent(list), [list]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return list.filter((p) => {
      if (namespace && p.namespace !== namespace) return false;
      if (role && p.role !== role) return false;
      if (!q) return true;
      // Namespace is searchable too: on an ambient install the namespace is
      // half of how a proxy is identified.
      return (
        p.name.toLowerCase().includes(q) || (p.namespace ?? "").toLowerCase().includes(q)
      );
    });
  }, [list, query, namespace, role]);

  if (list.length === 0) {
    return (
      <EmptyState icon={Waypoints} title="No mesh workloads in this window">
        Nothing here is classified as transport — no sidecars, waypoints or
        gateways have sent telemetry. If your proxies are running but missing,
        the classification is correctable per install through the hub&apos;s
        topology config, without waiting for a release.
      </EmptyState>
    );
  }

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-neutral px-4 py-3">
        <h2 className="text-sm font-medium">Proxies &amp; gateways</h2>
        <div className="flex flex-wrap items-center gap-2">
          {roles.length > 1 && (
            <Select
              ariaLabel="Filter by role"
              className="w-40"
              value={role}
              onChange={(v) => setMany({ role: v || undefined })}
              options={[
                { value: "", label: "All roles" },
                ...roles.map((r) => ({ value: r, label: roleLabel(r) })),
              ]}
            />
          )}
          {namespaces.length > 1 && (
            <Select
              ariaLabel="Filter by namespace"
              className="w-48"
              value={namespace}
              onChange={(v) => setMany({ ns: v || undefined })}
              options={[
                { value: "", label: "All namespaces" },
                ...namespaces.map((n) => ({ value: n, label: n })),
              ]}
            />
          )}
          <input
            type="search"
            aria-label="Filter proxies"
            placeholder="Filter…"
            value={query}
            onChange={(e) => setMany({ q: e.target.value || undefined })}
            className="w-48 rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs"
          />
        </div>
      </div>
      <ProxiesTable proxies={visible} onSelect={(name) => setMany({ proxy: name })} />
      {visible.length === 0 && (
        <p className="px-4 py-3 text-xs text-base-content/55">
          No proxy matches those filters.
        </p>
      )}
    </Card>
  );
}
