"use client";

import { Card } from "@/components/ui/card";
import { Stat } from "@/components/ui/stat";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/cn";
import type { ErrorStatsResponse } from "@/lib/api-types";
import { OccurrenceHistogram } from "./occurrence-histogram";

// The stats band: what the filtered issue set amounts to, above the rows it
// summarises. It answers the questions a list of fingerprints cannot — how many
// issues are new rather than merely present, how many came back after being
// resolved, and which services are producing the noise.
//
// Every number comes from /api/v1/errors/stats under the SAME filters as the
// list, so the band and the table can never state different totals.
export function ErrorStatsBand({
  stats,
  isLoading,
  onPickService,
  activeService,
}: {
  stats?: ErrorStatsResponse;
  isLoading: boolean;
  onPickService: (service: string) => void;
  activeService?: string;
}) {
  if (isLoading || !stats) {
    return (
      <Card
        className="flex min-h-32 items-center justify-center"
        data-testid="errors-stats"
      >
        <Spinner />
      </Card>
    );
  }

  return (
    <Card className="overflow-hidden" data-testid="errors-stats">
      <div className="grid gap-px border-b border-neutral bg-neutral sm:grid-cols-4">
        <Stat
          label="Issues"
          value={stats.issues.toLocaleString()}
          testid="stat-issues"
        />
        <Stat
          label="New in window"
          value={stats.newIssues.toLocaleString()}
          testid="stat-new"
        />
        <Stat
          label="Regressed"
          value={stats.regressed.toLocaleString()}
          tone={stats.regressed > 0 ? "warning" : undefined}
          testid="stat-regressed"
        />
        <Stat
          label="Events"
          value={stats.events.toLocaleString()}
          tone={stats.events > 0 ? "error" : undefined}
          testid="stat-events"
        />
      </div>

      <div className="flex flex-wrap gap-4 p-3">
        <section className="min-w-64 flex-1">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-base-content/50">
            Events over the window
          </h3>
          <OccurrenceHistogram points={stats.histogram} testid="errors-histogram" />
        </section>

        <section className="min-w-56">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-base-content/50">
            Top services
          </h3>
          {stats.topServices.length === 0 ? (
            <p className="text-xs text-base-content/50">No service produced an error here.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {stats.topServices.map((s) => {
                const active = s.service === activeService;
                return (
                  <li key={s.service}>
                    <button
                      type="button"
                      onClick={() => onPickService(s.service)}
                      aria-pressed={active}
                      data-testid={`top-service-${s.service}`}
                      title={
                        active
                          ? `Clear the ${s.service} filter`
                          : `Filter issues to ${s.service}`
                      }
                      className={cn(
                        "flex w-full items-center justify-between gap-3 rounded-md px-2 py-1 text-left text-xs transition-colors hover:bg-base-300",
                        active && "bg-primary/10 text-primary",
                      )}
                    >
                      <span className="truncate font-medium">{s.service}</span>
                      <span className="shrink-0 font-mono tabular-nums text-base-content/60">
                        {s.events.toLocaleString()}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </div>
    </Card>
  );
}
