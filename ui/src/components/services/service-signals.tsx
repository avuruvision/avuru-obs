"use client";

import { useRouter } from "next/navigation";
import Link from "next/link";
import { Bug, ScrollText } from "lucide-react";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { TraceList } from "@/components/traces/trace-list";
import { LogTable } from "@/components/logs/log-table";
import { IssueList } from "@/components/errors/issue-list";
import { useTraceSearch } from "@/hooks/use-traces-data";
import { useLogSearch } from "@/hooks/use-logs-data";
import { useErrorIssues } from "@/hooks/use-errors-data";
import { useTimeRange } from "@/hooks/use-time-range";

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

function ServiceLogs({ service }: { service: string }) {
  const { time } = useTimeRange();
  const logs = useLogSearch(time, { service });
  const pages = logs.data?.pages.map((p) => p.logs);
  const empty = !logs.isLoading && !pages?.some((p) => p.length > 0);

  if (empty) {
    return (
      <EmptyState icon={ScrollText} title="No logs in this window">
        This service emitted no log records here. Widen the time range, or check
        that its logs reach the collector.
      </EmptyState>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-base-content/50">
        Logs from this service ·{" "}
        <Link
          href={`/logs?service=${encodeURIComponent(service)}`}
          className="text-primary hover:underline"
        >
          open in Logs
        </Link>
      </p>
      <LogTable
        pages={pages}
        isLoading={logs.isLoading}
        hasNextPage={Boolean(logs.hasNextPage)}
        isFetchingNextPage={logs.isFetchingNextPage}
        fetchNextPage={() => logs.fetchNextPage()}
      />
    </div>
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
