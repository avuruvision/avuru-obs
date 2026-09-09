"use client";

import Link from "next/link";
import { useTimeRange } from "@/hooks/use-time-range";
import { useMeshWorkloadLogs } from "@/hooks/use-mesh-data";
import { useURLState } from "@/hooks/use-url-state";
import {
  SourcedLogs,
  sourceParam,
  useSourceSet,
  type SourceURLKeys,
} from "@/components/logs/sourced-logs";

// This page already spends `q` on the proxy list, so the toolbar's keys are
// prefixed rather than shared — two controls writing one key would fight.
const KEYS: SourceURLKeys = { q: "wlq", severity: "wlsev", source: "wlsrc" };

// A workload's logs from all three sources, one stream. The toolbar itself is
// shared with the service page: the two answer the same question about the
// same lines, and only the subject and the URL keys differ.
export function WorkloadLogs({
  namespace,
  name,
  waypoint,
}: {
  namespace: string;
  name: string;
  // "namespace/name" of the bound waypoint, handed to the hub for the case
  // where it cannot resolve the binding itself.
  waypoint?: string;
}) {
  const { time } = useTimeRange();
  const { get } = useURLState();
  const { enabled } = useSourceSet(KEYS.source);

  const logs = useMeshWorkloadLogs(time, true, namespace, name, {
    q: get(KEYS.q),
    severity: get(KEYS.severity),
    source: sourceParam(enabled),
    waypoint,
  });

  return (
    <div data-testid="mesh-workload-logs">
      <SourcedLogs
        query={{
          pages: logs.data?.pages.map((p) => p.logs),
          sources: logs.data?.pages[0]?.sources,
          isLoading: logs.isLoading,
          hasNextPage: Boolean(logs.hasNextPage),
          isFetchingNextPage: logs.isFetchingNextPage,
          fetchNextPage: () => logs.fetchNextPage(),
        }}
        keys={KEYS}
        searchLabel="Search workload logs"
        sourcesTestId="mesh-workload-log-sources"
        appLabel={name}
        downloadName={name}
        emptyText={
          <>
            Neither this workload nor the proxies carrying it logged anything
            here that matches. Widen the time range, or check that its logs
            reach the collector.
          </>
        }
        links={() => (
          <Link
            href={`/logs?service=${encodeURIComponent(name)}`}
            className="text-primary hover:underline"
          >
            open the app lines in Logs
          </Link>
        )}
      />
    </div>
  );
}
