"use client";

import { Card } from "@/components/ui/card";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { formatKgCo2e, formatPercent } from "@/lib/format";
import { cn } from "@/lib/cn";
import type { GreenBudget, GreenBurnPoint } from "@/lib/api-types";

// Status → semantic token mapping. daisyUI success/warning/error only — never
// a hardcoded hex (agent_docs/ui_patterns.md).
const STATUS: Record<GreenBudget["status"], { tone: BadgeProps["tone"]; bar: string; line: string; label: string }> = {
  ok: { tone: "success", bar: "bg-success", line: "text-success", label: "On track" },
  warn: { tone: "warning", bar: "bg-warning", line: "text-warning", label: "Warning" },
  exceeded: { tone: "error", bar: "bg-error", line: "text-error", label: "Exceeded" },
};

// Per-group monthly carbon budgets: usage against the ceiling, a burn-down of
// cumulative carbon, and the linear month-end projection. Budgets are
// config-defined (a ConfigMap the hub hot-reloads) — read-only here.
export function BudgetCards({
  budgets,
  alertingEnabled,
}: {
  budgets: GreenBudget[];
  alertingEnabled: boolean;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      {budgets.map((b) => (
        <BudgetCard key={b.name} budget={b} alertingEnabled={alertingEnabled} />
      ))}
    </div>
  );
}

function BudgetCard({ budget, alertingEnabled }: { budget: GreenBudget; alertingEnabled: boolean }) {
  const s = STATUS[budget.status];
  const pct = Math.min(100, budget.ratio * 100);
  const projectedPct = budget.monthlyKgCO2e > 0 ? budget.projectedKgCO2e / budget.monthlyKgCO2e : 0;

  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">{budget.name}</h3>
          <p className="truncate text-xs text-base-content/55">group: {budget.group}</p>
        </div>
        <Badge tone={s.tone}>{s.label}</Badge>
      </div>

      <div className="flex flex-col gap-1">
        <div className="flex items-baseline justify-between text-xs">
          <span className="font-mono">
            {formatKgCo2e(budget.usedKgCO2e)}
            <span className="text-base-content/50"> / {formatKgCo2e(budget.monthlyKgCO2e)}</span>
          </span>
          <span className={cn("font-mono", s.line)}>{formatPercent(budget.ratio)}</span>
        </div>
        <div
          className="h-2 w-full overflow-hidden rounded bg-base-300"
          role="progressbar"
          aria-valuenow={Math.round(pct)}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-label={`${budget.name} budget usage`}
        >
          <div className={cn("h-full", s.bar)} style={{ width: `${pct}%` }} />
        </div>
      </div>

      <BurnDown
        points={budget.burnDown ?? []}
        budget={budget.monthlyKgCO2e}
        lineClass={s.line}
      />

      <p className="text-xs text-base-content/60">
        Projected month-end:{" "}
        <span className={cn("font-mono", s.line)}>{formatKgCo2e(budget.projectedKgCO2e)}</span>
        {budget.monthlyKgCO2e > 0 && (
          <span className="text-base-content/45"> · {formatPercent(projectedPct)} of budget</span>
        )}
      </p>

      {!alertingEnabled && (
        <p className="border-t border-neutral/50 pt-2 text-[11px] text-base-content/45">
          Notifications require the alerting module — this budget shows status
          only until it is enabled.
        </p>
      )}
    </Card>
  );
}

// Cumulative-carbon burn-down: the used curve rising toward a dashed budget
// ceiling. Same axis-less inline-SVG recipe as RedChart (no chart dependency),
// with the budget line added so a crossing is visible at a glance.
function BurnDown({
  points,
  budget,
  lineClass,
}: {
  points: GreenBurnPoint[];
  budget: number;
  lineClass: string;
}) {
  const width = 240;
  const height = 56;
  const pad = 4;

  if (points.length < 2) {
    return (
      <div className="flex h-14 items-center justify-center rounded bg-base-300/40 text-[11px] text-base-content/35">
        not enough data yet
      </div>
    );
  }

  const values = points.map((p) => p.kgCO2e);
  const max = Math.max(...values, budget) || 1;
  const step = (width - pad * 2) / (points.length - 1);
  const y = (v: number) => pad + (1 - v / max) * (height - pad * 2);
  const coords = values.map((v, i) => `${(pad + i * step).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const budgetY = y(budget).toFixed(1);

  return (
    <svg
      width="100%"
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      role="img"
      aria-label="carbon burn-down"
      className="rounded bg-base-300/40"
    >
      {budget > 0 && (
        <line
          x1={pad}
          x2={width - pad}
          y1={budgetY}
          y2={budgetY}
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="3 3"
          className="text-base-content/40"
          vectorEffect="non-scaling-stroke"
        />
      )}
      <polyline
        points={coords}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        className={lineClass}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
