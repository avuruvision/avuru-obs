"use client";

import { useMemo } from "react";
import { useHealthGroups } from "@/hooks/use-health-data";
import type { TimeParams } from "@/lib/query-keys";
import type { HealthGroup, HealthStatus } from "@/lib/api-types";

export interface ServiceHealth {
  status: HealthStatus;
  reason: string;
  group: string;
  tier: string;
}

// Severity order for the worst-of pick below. Mirrors hub/internal/health's
// ordering, where idle/unknown sit BELOW healthy so a quiet service never makes
// anything look worse. This is ordering only — the thresholds that produce a
// status stay in the hub, where they are configurable per group.
const SEVERITY: Record<string, number> = { healthy: 1, degraded: 2, down: 3 };
const severity = (s: string) => SEVERITY[s] ?? 0;

// Per-service health for the map's status rings, flattened from the group
// rollup the Service Health screen already reads. effectiveStatus, not
// baseStatus: a service dragged down by a dependency must read the same on the
// map as it does on the health board.
//
// `enabled` is false on an install without the service-health module — the
// endpoint does not exist there, and the map falls back to error presence.
export function useServiceHealthStatus(
  time: TimeParams,
  includeAux: boolean,
  enabled: boolean,
) {
  const { data, isLoading } = useHealthGroups(time, includeAux, enabled);

  const byService = useMemo(() => {
    const m = new Map<string, ServiceHealth>();
    for (const g of data?.groups ?? []) {
      for (const mem of g.members) {
        // A service can belong to more than one group; the worst status wins so
        // the ring never under-reports.
        const prev = m.get(mem.service);
        if (prev && severity(prev.status) >= severity(mem.effectiveStatus)) continue;
        m.set(mem.service, {
          status: mem.effectiveStatus,
          reason: mem.reason,
          group: g.name,
          tier: g.tier,
        });
      }
    }
    return m;
  }, [data]);

  const groups: HealthGroup[] = useMemo(() => data?.groups ?? [], [data]);

  return { byService, groups, isLoading };
}
