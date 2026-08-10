"use client";

import Link from "next/link";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { formatMs, formatPercent, formatRate } from "@/lib/format";
import { statusDotClass, statusLabel, statusTone } from "@/lib/health-status";
import type { HealthGroup, ServiceStats } from "@/lib/api-types";

// Band 1 of the Dashboard: one card per service group, with the RED numbers the
// group already carries. Groups come from the service-health module (W3 made
// them UI-authored), so when that module is off the band falls back to the
// service inventory — the band never disappears, it just loses the grouping.
//
// The fallback cards carry NO status: health semantics (thresholds, critical
// dependency propagation) belong to the service-health module, and inventing a
// second set of thresholds here would put two different answers on screen for
// the same question. Error rate still reads red when non-zero, which is the
// same convention the service map already uses for its node fill.

interface Tile {
  key: string;
  title: string;
  // tier for a group, namespace-ish caption for a service; may be empty.
  caption?: string;
  status?: string;
  ratePerSec: number;
  errorRate: number;
  p95Ms: number;
  href: string;
  auto?: boolean;
}

function groupTile(g: HealthGroup): Tile {
  return {
    key: `group:${g.name}`,
    title: g.name,
    caption: g.tier,
    status: g.status,
    ratePerSec: g.ratePerSec,
    errorRate: g.errorRate,
    p95Ms: g.p95Ms,
    href: `/health?member=${encodeURIComponent(g.members[0]?.service ?? "")}`,
    auto: g.source === "auto",
  };
}

function serviceTile(s: ServiceStats): Tile {
  return {
    key: `service:${s.name}`,
    title: s.name,
    ratePerSec: s.ratePerSec,
    errorRate: s.errorRate,
    p95Ms: s.p95Ms,
    href: `/traces?service=${encodeURIComponent(s.name)}&tab=traces`,
  };
}

// Cards are the estate at a glance, so the band stays one screenful: the
// busiest N, with the rest a count. Groups sort worst-status-first (a down
// group must never be the one pushed off the end), services by traffic.
const MAX_TILES = 8;
const STATUS_RANK: Record<string, number> = { down: 3, degraded: 2, idle: 1, healthy: 0 };

export function SummaryCards({
  groups,
  services,
}: {
  groups?: HealthGroup[];
  services: ServiceStats[];
}) {
  const grouped = groups !== undefined;
  const all: Tile[] = grouped
    ? [...groups]
        .sort(
          (a, b) =>
            (STATUS_RANK[b.status] ?? 0) - (STATUS_RANK[a.status] ?? 0) ||
            b.ratePerSec - a.ratePerSec,
        )
        .map(groupTile)
    : [...services].sort((a, b) => b.ratePerSec - a.ratePerSec).map(serviceTile);

  const tiles = all.slice(0, MAX_TILES);
  const hidden = all.length - tiles.length;

  return (
    <section className="flex flex-col gap-2" data-testid="dashboard-summary">
      <div className="flex items-center gap-2 border-b border-neutral pb-1.5">
        <h2 className="text-sm font-semibold">{grouped ? "Service groups" : "Services"}</h2>
        <span className="text-xs text-base-content/45">
          {grouped
            ? "health, traffic and latency per group"
            : "busiest services — enable service health to group them"}
        </span>
        {hidden > 0 && (
          <Link
            href={grouped ? "/health" : "/services"}
            className="ml-auto text-xs text-primary hover:underline"
          >
            +{hidden} more →
          </Link>
        )}
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {tiles.map((t) => (
          <Link key={t.key} href={t.href} className="block">
            <Card className="flex h-full flex-col gap-2 p-3 transition-colors hover:border-primary/50">
              <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                  {t.status && (
                    <span
                      className={cn("h-2.5 w-2.5 shrink-0 rounded-full", statusDotClass(t.status))}
                      aria-hidden
                    />
                  )}
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="truncate text-sm font-semibold">{t.title}</span>
                      {t.auto && (
                        <span className="rounded bg-base-300 px-1 text-[10px] text-base-content/50">
                          auto
                        </span>
                      )}
                    </div>
                    {t.caption && (
                      <p className="truncate text-xs text-base-content/55">{t.caption}</p>
                    )}
                  </div>
                </div>
                {t.status && <Badge tone={statusTone(t.status)}>{statusLabel(t.status)}</Badge>}
              </div>

              <dl className="mt-auto grid grid-cols-3 gap-1 text-xs">
                <div>
                  <dt className="text-base-content/45">Rate</dt>
                  <dd className="tabular-nums">{formatRate(t.ratePerSec)}</dd>
                </div>
                <div>
                  <dt className="text-base-content/45">p95</dt>
                  <dd className="tabular-nums">{formatMs(t.p95Ms)}</dd>
                </div>
                <div>
                  <dt className="text-base-content/45">Errors</dt>
                  <dd className={cn("tabular-nums", t.errorRate > 0 && "text-error")}>
                    {formatPercent(t.errorRate)}
                  </dd>
                </div>
              </dl>
            </Card>
          </Link>
        ))}
      </div>
    </section>
  );
}
