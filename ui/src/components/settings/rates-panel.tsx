"use client";

import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useAuth } from "@/hooks/use-auth";
import { useRates, useSaveRates, useClearRates } from "@/hooks/use-rates";
import type { RateModelPrice, RateTable } from "@/lib/api-types";

// The one rate table: what this estate costs, written down once.
//
// Before this there were two, in two mechanisms — AI prices in a mounted
// ConfigMap, compute rates in three environment variables read once at
// startup — so the same operator declared their costs twice, in two formats,
// one of which needed a pod restart. Both now resolve here.
//
// Chart-declared values stay READ-ONLY, exactly as service groups does it: the
// hub merges them underneath, so offering an edit would promise a change a
// `helm upgrade` silently reverts. They are still shown, because an operator
// who cannot see them has no way to understand why their own entry did or did
// not change anything.
//
// The write gate is canAdminister, not isAdmin: the hub's securedAdmin serves
// every caller on an install running without authentication, so isAdmin would
// hide the editor from exactly the installs the hub is already letting through.
export function RatesPanel() {
  const { canAdminister } = useAuth();
  const { data, isLoading } = useRates();
  const save = useSaveRates();
  const clear = useClearRates();

  // null means "not edited yet", so the draft falls through to whatever the
  // server last returned. Deriving it rather than syncing it in an effect is
  // what keeps a refetch from discarding what is being typed.
  const [edited, setEdited] = useState<RateTable | null>(null);

  if (isLoading || !data) return <CenteredSpinner />;

  const draft: RateTable = edited ?? data.overlay ?? {};
  const setDraft = (t: RateTable) => setEdited(t);

  const chart = data.chart ?? {};
  const chartModels = chart.models ?? [];
  const models = draft.models ?? [];
  const busy = save.isPending || clear.isPending;

  const setModel = (i: number, patch: Partial<RateModelPrice>) =>
    setDraft({
      ...draft,
      models: models.map((m, j) => (j === i ? { ...m, ...patch } : m)),
    });

  return (
    <div className="space-y-4" data-testid="rates-panel">
      <Card>
        <CardHeader>
          <CardTitle>Currency and compute</CardTitle>
          <span className="text-xs text-base-content/50">
            what a core-hour and a GiB-hour cost
          </span>
        </CardHeader>
        <div className="grid gap-3 p-4 sm:grid-cols-3">
          <Field
            label="Currency"
            placeholder={chart.currency || "EUR"}
            value={draft.currency ?? ""}
            disabled={!canAdminister || busy}
            onChange={(v) => setDraft({ ...draft, currency: v })}
          />
          <Field
            label="CPU core-hour"
            placeholder={String(chart.compute?.cpuCoreHour ?? 0)}
            value={draft.compute?.cpuCoreHour ?? ""}
            disabled={!canAdminister || busy}
            numeric
            onChange={(v) =>
              setDraft({
                ...draft,
                compute: { ...draft.compute, cpuCoreHour: num(v) },
              })
            }
          />
          <Field
            label="Memory GiB-hour"
            placeholder={String(chart.compute?.memGiBHour ?? 0)}
            value={draft.compute?.memGiBHour ?? ""}
            disabled={!canAdminister || busy}
            numeric
            onChange={(v) =>
              setDraft({
                ...draft,
                compute: { ...draft.compute, memGiBHour: num(v) },
              })
            }
          />
        </div>
        {/* Both compute rates are needed before money appears anywhere: a total
            built from CPU alone would look complete while omitting memory. */}
        <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/55">
          Cost is shown only when both compute rates are set. Leave them empty
          to report usage without money.
        </p>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Model prices</CardTitle>
          <span className="text-xs text-base-content/50">
            per million tokens, the way providers publish them
          </span>
          {canAdminister && (
            <Button
              size="sm"
              className="ml-auto"
              disabled={busy}
              onClick={() =>
                setDraft({ ...draft, models: [...models, { model: "" }] })
              }
            >
              <Plus className="mr-1 h-3.5 w-3.5" /> Add
            </Button>
          )}
        </CardHeader>

        <div className="divide-y divide-neutral">
          {chartModels.map((m) => (
            <ChartRow key={`chart-${m.model}`} price={m} />
          ))}
          {models.map((m, i) => (
            <div
              key={`overlay-${i}`}
              className="grid gap-2 p-3 sm:grid-cols-[2fr_1fr_1fr_auto]"
            >
              <Field
                label="Model"
                placeholder="gpt-4o"
                value={m.model}
                disabled={!canAdminister || busy}
                onChange={(v) => setModel(i, { model: v })}
              />
              <Field
                label="Input / 1M"
                value={m.inputPer1MTokens ?? ""}
                disabled={!canAdminister || busy}
                numeric
                onChange={(v) => setModel(i, { inputPer1MTokens: num(v) })}
              />
              <Field
                label="Output / 1M"
                value={m.outputPer1MTokens ?? ""}
                disabled={!canAdminister || busy}
                numeric
                onChange={(v) => setModel(i, { outputPer1MTokens: num(v) })}
              />
              {canAdminister && (
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Remove ${m.model || "price"}`}
                  className="self-end"
                  disabled={busy}
                  onClick={() =>
                    setDraft({
                      ...draft,
                      models: models.filter((_, j) => j !== i),
                    })
                  }
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              )}
            </div>
          ))}
          {!chartModels.length && !models.length && (
            <p className="p-4 text-xs text-base-content/45">
              No prices declared. The AI screens report token counts and say
              that is what they are reporting — there is no bundled price table,
              which would be stale within a month while looking exactly as
              authoritative as a number you typed yourself.
            </p>
          )}
        </div>

        {canAdminister && (
          <div className="flex items-center gap-2 border-t border-neutral p-3">
            <Button disabled={busy} onClick={() => save.mutate(draft)}>
              Save
            </Button>
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => {
                clear.mutate(undefined, { onSuccess: () => setEdited(null) });
              }}
            >
              Reset to chart values
            </Button>
            {save.isError && (
              <span className="text-xs text-error">{String(save.error)}</span>
            )}
            {data.updatedBy && (
              <span className="ml-auto text-xs text-base-content/45">
                Last edited by {data.updatedBy}
              </span>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}

// A chart-declared price, shown and not editable. Hiding it would leave an
// operator unable to explain a number the screens are already using.
function ChartRow({ price }: { price: RateModelPrice }) {
  return (
    <div className="flex flex-wrap items-center gap-2 p-3 text-sm">
      <span className="font-mono">{price.model}</span>
      <Badge tone="neutral">chart</Badge>
      <span className="ml-auto tabular-nums text-base-content/60">
        {price.inputPer1MTokens ?? 0} in / {price.outputPer1MTokens ?? 0} out
      </span>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  disabled,
  placeholder,
  numeric,
}: {
  label: string;
  value: string | number;
  onChange: (v: string) => void;
  disabled?: boolean;
  placeholder?: string;
  numeric?: boolean;
}) {
  return (
    <label className="flex flex-col gap-1 text-xs">
      <span className="text-base-content/60">{label}</span>
      <input
        className="rounded-md border border-neutral bg-base-100 px-2 py-1 text-sm disabled:opacity-60"
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        inputMode={numeric ? "decimal" : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

// An empty field means "not set", which is not the same as zero: zero is a
// declared price of nothing, and the hub refuses an entry that states neither.
function num(v: string): number | undefined {
  const t = v.trim();
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}
