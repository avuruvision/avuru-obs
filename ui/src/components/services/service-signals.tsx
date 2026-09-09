"use client";

import { useRouter } from "next/navigation";
import Link from "next/link";
import { Bug } from "lucide-react";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { TraceList } from "@/components/traces/trace-list";
import {
  SourcedLogs,
  sourceParam,
  useSourceSet,
  type SourceURLKeys,
} from "@/components/logs/sourced-logs";
import { IssueList } from "@/components/errors/issue-list";
import { useTraceSearch } from "@/hooks/use-traces-data";
import { useServiceLogs } from "@/hooks/use-logs-data";
import { useErrorIssues } from "@/hooks/use-errors-data";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";

export type SignalTab = "overview" | "traces" | "logs" | "errors";

// The raw signals for one service, each reusing the component that owns it on
// its own screen. Nothing is re-implemented here: a second trace list would be
// a second set of rendering rules to keep in step with the first.
//
// Selecting a row leaves for the screen that owns the detail rather than
// embedding it. The trace workspace and the issue panel are large, stateful
// surfaces, and a service page that swallowed both would be two screens wearing
// one URL.
export function ServiceSignals({
  service,
  tab,
  includeAux,
}: {
  service: string;
  tab: Exclude<SignalTab, "overview">;
  includeAux: boolean;
}) {
  if (tab === "traces") return <ServiceTraces service={service} includeAux={includeAux} />;
  if (tab === "logs") return <ServiceLogs service={service} />;
  return <ServiceErrors service={service} />;
}

function ServiceTraces({ service, includeAux }: { service: string; includeAux: boolean }) {
  const { time } = useTimeRange();
  const router = useRouter();
  const search = useTraceSearch(time, { service, includeAux });

  return (
    <TraceList
      pages={search.data?.pages.map((p) => p.traces)}
      isLoading={search.isLoading}
      hasNextPage={Boolean(search.hasNextPage)}
      isFetchingNextPage={search.isFetchingNextPage}
      fetchNextPage={() => search.fetchNextPage()}
      onSelect={(traceId) =>
        router.push(
          `/traces?service=${encodeURIComponent(service)}&tab=traces&trace=${encodeURIComponent(traceId)}`,
        )
      }
    />
  );
}

// This screen spends its URL on the selection (?service=), so the toolbar can
// have the plain keys.
const LOG_KEYS: SourceURLKeys = { q: "q", severity: "severity", source: "src" };

function ServiceLogs({ service }: { service: string }) {
  const { time } = useTimeRange();
  const { get } = useURLState();
  const { enabled } = useSourceSet(LOG_KEYS.source);

  const logs = useServiceLogs(time, service, {
    q: get(LOG_KEYS.q),
    severity: get(LOG_KEYS.severity),
    source: sourceParam(enabled),
  });
  const sources = logs.data?.pages[0]?.sources;

  return (
    <SourcedLogs
      query={{
        pages: logs.data?.pages.map((p) => p.logs),
        sources,
        isLoading: logs.isLoading,
        hasNextPage: Boolean(logs.hasNextPage),
        isFetchingNextPage: logs.isFetchingNextPage,
        fetchNextPage: () => logs.fetchNextPage(),
      }}
      keys={LOG_KEYS}
      appLabel={service}
      downloadName={service}
      emptyText={
        <>
          Neither this service nor the proxies carrying it logged anything here
          that matches. Widen the time range, or check that its logs reach the
          collector.
        </>
      }
      links={(s) => (
        <>
          <Link
            href={`/logs?service=${encodeURIComponent(service)}`}
            className="text-primary hover:underline"
          >
            open in Logs
          </Link>
          {s.workload && s.namespace && (
            <Link
              href={`/mesh?view=workloads&wl=${encodeURIComponent(`${s.namespace}/${s.workload}`)}&wltab=logs`}
              className="text-primary hover:underline"
            >
              open the workload
            </Link>
          )}
        </>
      )}
    />
  );
}

function ServiceErrors({ service }: { service: string }) {
  const { time } = useTimeRange();
  const router = useRouter();
  const issues = useErrorIssues(time, { service });

  if (issues.isLoading) return <CenteredSpinner />;
  const rows = issues.data?.issues ?? [];
  if (!rows.length) {
    return (
      <EmptyState icon={Bug} title="No open issues">
        Nothing was grouped into an error issue for this service in this window.
      </EmptyState>
    );
  }
  return (
    <IssueList
      issues={rows}
      selected={null}
      onSelect={(fingerprint) =>
        router.push(`/errors?issue=${encodeURIComponent(fingerprint)}`)
      }
    />
  );
}
