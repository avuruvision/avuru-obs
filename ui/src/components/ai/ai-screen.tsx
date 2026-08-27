"use client";

import Link from "next/link";
import { Bot, ShieldAlert, TriangleAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useTimeRange } from "@/hooks/use-time-range";
import { useAICallers, useAIModels } from "@/hooks/use-ai-data";
import { formatMs, formatPercent } from "@/lib/format";
import type { AICaller, AIUsage } from "@/lib/api-types";

// AI observability: what the applications in this estate asked models to do.
//
// Everything here is read from the gen_ai.* attributes on spans already being
// stored — no collection, no schema. The screen's job is to know what the
// numbers MEAN, which is what a generic breakdown of the same attribute
// cannot: that the two token columns are priced differently, that a truncated
// answer is not a failed one, and that a missing count is not a zero.
export function AIScreen() {
  const { time } = useTimeRange();
  const models = useAIModels(time);
  const callers = useAICallers(time);

  if (models.isLoading) return <CenteredSpinner />;
  if (models.isError) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read model calls.
      </Card>
    );
  }

  const data = models.data;
  const rows = data?.models ?? [];
  const total = data?.total;
  const priced = !!data?.priced;
  const currency = data?.currency ?? "";

  if (!total || total.calls === 0) {
    return (
      <EmptyState icon={Bot} title="No model calls in this window">
        This reads the <code className="rounded bg-base-300 px-1">gen_ai.*</code>{" "}
        attributes an instrumented application already sends — nothing extra is
        collected. Point an LLM SDK’s OpenTelemetry instrumentation at this
        install and its calls appear here with the rest of its traces.
      </EmptyState>
    );
  }

  const tokens = total.inputTokens + total.outputTokens;

  return (
    <div className="flex flex-col gap-5">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Model calls</CardTitle>
          {!priced && (
            <span className="text-xs text-base-content/50">
              no prices configured — set{" "}
              <span className="font-mono">ai.prices</span> to see money
            </span>
          )}
        </CardHeader>
        <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
          <Stat label="Calls" value={compact(total.calls)} testid="ai-calls" />
          <Stat
            label="Tokens"
            value={`${compact(total.inputTokens)} in · ${compact(total.outputTokens)} out`}
            testid="ai-tokens"
          />
          {priced ? (
            <Stat
              label="Estimated cost"
              value={`${money(total.cost ?? 0)} ${currency}`.trim()}
              testid="ai-cost"
            />
          ) : (
            <Stat label="Total tokens" value={compact(tokens)} testid="ai-cost" />
          )}
          <Stat label="Models" value={String(data?.modelCount ?? rows.length)} />
        </div>

        <ContentWarning calls={total.callsWithContent} />
        <Coverage total={total} unpriced={priced ? (data?.unpricedModels ?? []) : []} />
      </Card>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>By model</CardTitle>
          <span className="text-xs text-base-content/50">
            busiest first · latency is the whole call, not time to first token
          </span>
        </CardHeader>
        <div className="overflow-x-auto">
          <table className="table-dense w-full text-sm" data-testid="ai-models">
            <thead>
              <tr className="border-y border-neutral text-left">
                <th>Model</th>
                <th className="text-right">Calls</th>
                <th className="text-right">Failed</th>
                <th className="text-right">Truncated</th>
                <th className="text-right">Tokens in</th>
                <th className="text-right">Tokens out</th>
                <th className="text-right">p95</th>
                {priced && <th className="text-right">Cost {currency}</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((m) => (
                <ModelRow key={m.model || "(unknown)"} m={m} priced={priced} />
              ))}
              {data?.other && (
                // The tail is arithmetic on the window's totals, so the parts
                // sum to the whole. Its latency cannot be recovered by
                // subtraction, so it is not drawn rather than drawn as zero.
                <tr className="border-b border-neutral/50 text-base-content/60 last:border-0">
                  <td className="italic">everything else</td>
                  <td className="text-right">{compact(data.other.calls)}</td>
                  <td className="text-right">{compact(data.other.errors + data.other.refused)}</td>
                  <td className="text-right">{compact(data.other.truncated)}</td>
                  <td className="text-right">{compact(data.other.inputTokens)}</td>
                  <td className="text-right">{compact(data.other.outputTokens)}</td>
                  <td className="text-right">—</td>
                  {priced && <td className="text-right">—</td>}
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
          Grouped by the model that <em>answered</em>. To slice the same calls
          any other way — by route, namespace or business tag —{" "}
          <Link
            href="/traces?tab=breakdown&groupBy=attribute:gen_ai.request.model&scope=all"
            className="text-primary hover:underline"
          >
            open them in the trace breakdown
          </Link>
          .
        </p>
      </Card>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Who is calling</CardTitle>
          <span className="text-xs text-base-content/50">
            the same calls, with an owner
          </span>
        </CardHeader>
        {callers.data?.callers.length ? (
          <div className="overflow-x-auto">
            <table className="table-dense w-full text-sm" data-testid="ai-callers">
              <thead>
                <tr className="border-y border-neutral text-left">
                  <th>Service</th>
                  <th>Model</th>
                  <th className="text-right">Calls</th>
                  <th className="text-right">Tokens</th>
                  {priced && <th className="text-right">Cost {currency}</th>}
                </tr>
              </thead>
              <tbody>
                {callers.data.callers.map((c) => (
                  <CallerRow key={`${c.service}/${c.model}`} c={c} priced={priced} />
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="p-4 text-xs text-base-content/45">
            No calling service could be resolved in this window.
          </p>
        )}
      </Card>
    </div>
  );
}

// The exposure report — the one thing this screen can tell an operator that
// nothing else in the product can.
//
// Prompts and completions reach storage only because an application's own SDK
// was configured to capture them. The gateway drops them by default, so
// anything counted here got past that: either redaction was turned off, or it
// is arriving under a key the pattern does not know. Either way the text is in
// the trace store, under the ordinary retention, readable by every Viewer.
function ContentWarning({ calls }: { calls: number }) {
  if (calls <= 0) return null;
  return (
    <div
      className="flex items-start gap-2 border-t border-neutral bg-warning/10 p-2.5 text-xs"
      data-testid="ai-content-warning"
    >
      <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" aria-hidden />
      <span>
        <strong>
          {compact(calls)} call{calls === 1 ? "" : "s"} arrived carrying prompt or
          completion text
        </strong>{" "}
        — user content, stored under your trace retention and readable by anyone
        who can open a trace. Nothing here displays it. Drop it at the gateway
        with{" "}
        <code className="rounded bg-base-300 px-1">
          gateway.genai.redactContent=true
        </code>
        , or stop capturing it in the application, which is the real fix.
      </span>
    </div>
  );
}

// What the numbers above do NOT cover. Each line is a way the screen would
// otherwise be quietly wrong, so they are stated rather than left to be
// inferred from a suspiciously round total.
function Coverage({ total, unpriced }: { total: AIUsage; unpriced: string[] }) {
  const notes: string[] = [];
  if (total.callsWithoutUsage > 0) {
    notes.push(
      `${compact(total.callsWithoutUsage)} of ${compact(total.calls)} calls reported no token usage and are excluded from the token and cost totals`,
    );
  }
  if (total.callsFromRequestModel > 0) {
    const n = total.callsFromRequestModel;
    notes.push(
      `${compact(n)} ${n === 1 ? "is" : "are"} attributed to the model that was requested, because nothing said what answered`,
    );
  }
  if (unpriced.length > 0) {
    notes.push(
      `no price is declared for ${unpriced.join(", ")}, so the cost above is a floor`,
    );
  }
  if (!notes.length) return null;
  return (
    <div
      className="flex items-start gap-2 border-t border-neutral p-2.5 text-xs text-base-content/55"
      data-testid="ai-coverage"
    >
      <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-base-content/40" aria-hidden />
      <span>{notes.join("; ")}.</span>
    </div>
  );
}

function ModelRow({ m, priced }: { m: AIUsage; priced: boolean }) {
  const failed = m.errors + m.refused;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td>
        <span className="font-medium">{m.model || "(model not reported)"}</span>
        {m.provider && (
          <span className="ml-1.5 text-xs text-base-content/45">{m.provider}</span>
        )}
        {m.callsFromRequestModel > 0 && m.callsFromRequestModel === m.calls && (
          <Badge tone="warning" title="No response model was reported; this is what was asked for">
            requested
          </Badge>
        )}
      </td>
      <td className="text-right">{compact(m.calls)}</td>
      <td className={`text-right ${failed > 0 ? "text-error" : "text-base-content/60"}`}>
        {failed > 0 ? `${compact(failed)} · ${formatPercent(failed / m.calls)}` : "—"}
      </td>
      {/* Truncation is not failure, so it never wears the error colour. */}
      <td className="text-right text-base-content/60">
        {m.truncated > 0 ? compact(m.truncated) : "—"}
      </td>
      <td className="text-right">{compact(m.inputTokens)}</td>
      <td className="text-right">{compact(m.outputTokens)}</td>
      <td className="text-right">{formatMs(m.p95Ms)}</td>
      {priced && (
        <td className="text-right">
          {m.cost === undefined ? (
            <span title="No rate declared for this model">—</span>
          ) : (
            <>
              {money(m.cost)}
              {m.pricedByPrefix && (
                <span
                  className="ml-1 text-base-content/40"
                  title="Priced by a prefix rule, not by an entry naming this exact model"
                >
                  ≈
                </span>
              )}
            </>
          )}
        </td>
      )}
    </tr>
  );
}

function CallerRow({ c, priced }: { c: AICaller; priced: boolean }) {
  // Every call in this row reported no usage. There is no token total to show
  // and no cost to state — a zero in either column would read as free.
  const noUsage = c.callsWithoutUsage >= c.calls;
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium">{c.service}</td>
      <td className="text-base-content/70">{c.model || "(model not reported)"}</td>
      <td className="text-right">{compact(c.calls)}</td>
      <td className="text-right">
        {noUsage ? (
          <span title="No token usage was reported for these calls">—</span>
        ) : (
          compact(c.inputTokens + c.outputTokens)
        )}
      </td>
      {priced && (
        <td className="text-right">{c.cost === undefined ? "—" : money(c.cost)}</td>
      )}
    </tr>
  );
}

function Stat({ label, value, testid }: { label: string; value: string; testid?: string }) {
  return (
    <div className="bg-base-200 p-3" data-testid={testid}>
      <p className="text-xs uppercase tracking-wider text-base-content/50">{label}</p>
      <p className="text-sm font-semibold">{value}</p>
    </div>
  );
}

// A real cost below a cent must not print as 0.00 — on a screen whose whole
// argument is that "absent" and "zero" are different claims, rounding a charge
// down to nothing is the same mistake in miniature.
function money(v: number): string {
  if (v > 0 && v < 0.005) return "<0.01";
  return v.toFixed(2);
}

// Token counts run to the millions; a raw integer in a table cell is unreadable
// and a locale separator is worse at a glance.
function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  if (n >= 10_000) return `${(n / 1000).toFixed(0)}k`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}
