"use client";

import { Info } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useTimeRange } from "@/hooks/use-time-range";
import { useGreenSummary, useGreenBudgets } from "@/hooks/use-green-data";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { formatGco2e, formatMgPerReq, formatPercent, formatWh } from "@/lib/format";
import type { GreenFactors, GreenTotals } from "@/lib/api-types";
import { ServiceEnergyTable } from "./service-energy-table";
import { BudgetCards } from "./budget-cards";
import { ExportPanel } from "./export-panel";
import { GreenEmptyState } from "./green-empty-state";

// Carbon dashboard orchestrator: the factors in force (with a methodology
// popover), the headline totals, the per-service table, budgets, and the CSRD
// export. Energy is a hardware signal (RAPL) — an install with none is expected,
// so a no-data summary delegates to the teaching empty state.
export function GreenScreen() {
  const { time } = useTimeRange();
  const summary = useGreenSummary(time);
  const budgets = useGreenBudgets();
  const alertingEnabled = useModuleEnabled("alerting");

  if (summary.isLoading) return <CenteredSpinner />;

  // An error is a hub outage, NOT missing hardware — don't hand the operator the
  // RAPL empty state (a Helm command that misdiagnoses a 500 as no-RAPL). The
  // teaching empty state is reserved for the real success-but-no-data case.
  if (summary.isError) {
    return <HubUnreachable what="energy & carbon data" />;
  }

  const data = summary.data;
  const services = data?.services ?? [];
  const totals = data?.totals;
  const totalWh = totals ? totals.attributedWh + totals.unattributedWh : 0;

  if (!data || !services.length || totalWh <= 0) {
    return <GreenEmptyState />;
  }

  const totalRequests = services.reduce((sum, s) => sum + (s.requests ?? 0), 0);
  const avgMg = totalRequests > 0 ? (totals!.gco2e * 1000) / totalRequests : undefined;
  const budgetList = budgets.data?.budgets ?? [];

  return (
    <div className="flex flex-col gap-5">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Energy &amp; carbon</CardTitle>
          <MethodologyDetails factors={data.factors} totals={totals!} />
        </CardHeader>

        <FactorsLine factors={data.factors} />

        <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
          <StatTile label="Total energy" value={formatWh(totalWh)} />
          <StatTile label="Total carbon" value={formatGco2e(totals!.gco2e)} />
          <StatTile label="Avg intensity" value={formatMgPerReq(avgMg)} />
          <StatTile label="Attribution coverage" value={formatPercent(totals!.coverage)} />
        </div>
      </Card>

      <div className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-base-content/80">Per-service</h2>
        <ServiceEnergyTable services={services} />
      </div>

      {(budgets.isError || budgetList.length > 0) && (
        <div className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold text-base-content/80">Carbon budgets</h2>
          {budgets.isError ? (
            <Card className="p-4 text-center text-sm text-error">
              Couldn&rsquo;t reach the hub to read carbon budgets.
            </Card>
          ) : (
            <BudgetCards budgets={budgetList} alertingEnabled={alertingEnabled} />
          )}
        </div>
      )}

      <ExportPanel time={time} />
    </div>
  );
}

// Hub-unreachable message — mirrors the codebase's outage pattern
// (settings/system-status.tsx). Distinct from GreenEmptyState so an outage is
// never dressed up as a missing-RAPL diagnosis.
function HubUnreachable({ what }: { what: string }) {
  return (
    <Card className="p-8 text-center text-sm text-error">
      Couldn&rsquo;t reach the hub to read {what}.
    </Card>
  );
}

function StatTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-base-200 p-3">
      <p className="text-xs uppercase tracking-wider text-base-content/50">{label}</p>
      <p className="font-mono text-sm font-semibold">{value}</p>
    </div>
  );
}

const FACTOR_ITEMS = (f: GreenFactors): { label: string; value: string }[] => [
  { label: "Grid intensity", value: `${Math.round(f.gridIntensity)} gCO2e/kWh` },
  { label: "Source", value: f.intensitySource },
  { label: "PUE", value: f.pue.toFixed(2) },
  { label: "Dataset", value: f.dataset },
];

function FactorsLine({ factors }: { factors: GreenFactors }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-neutral px-4 py-2.5 text-xs">
      {FACTOR_ITEMS(factors).map((item) => (
        <span key={item.label} className="text-base-content/55">
          {item.label}: <span className="font-mono text-base-content/80">{item.value}</span>
        </span>
      ))}
    </div>
  );
}

// Methodology disclosure — a native <details> so it is keyboard-operable with
// no dependency. States exactly how a number was produced, matching the audit
// block the CSRD export embeds.
function MethodologyDetails({ factors, totals }: { factors: GreenFactors; totals: GreenTotals }) {
  return (
    <details className="relative">
      <summary className="flex cursor-pointer list-none items-center gap-1 text-xs text-base-content/50 hover:text-base-content">
        <Info className="h-3.5 w-3.5" aria-hidden /> Methodology
      </summary>
      <div className="absolute right-0 z-20 mt-2 w-80 rounded-lg border border-neutral bg-base-100 p-3 text-xs leading-relaxed [box-shadow:var(--shadow-card-hover)]">
        <p className="mb-2 font-mono text-base-content/80">gCO2e = Wh × intensity/1000 × PUE</p>
        <dl className="flex flex-col gap-1 text-base-content/60">
          <Row k="Grid intensity" v={`${factors.gridIntensity} gCO2e/kWh`} />
          <Row k="Factor source" v={factors.intensitySource} />
          <Row k="PUE" v={factors.pue.toFixed(2)} />
          <Row k="Factor dataset" v={factors.dataset} />
          <Row k="Coverage" v={formatPercent(totals.coverage)} />
        </dl>
        <p className="mt-2 border-t border-neutral/50 pt-2 text-base-content/50">
          {formatWh(totals.unattributedWh)} could not be mapped to a workload and
          is reported separately as <span className="italic">(unattributed)</span>.
          Intensity uses static annual averages, not hour-by-hour grid data.
        </p>
      </div>
    </details>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between gap-3">
      <dt>{k}</dt>
      <dd className="text-right font-mono text-base-content/80">{v}</dd>
    </div>
  );
}
