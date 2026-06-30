"use client";

import { FilterX } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import type { TraceFilters } from "@/hooks/use-traces-data";

const INPUT =
  "h-9 w-full rounded-lg border border-neutral bg-base-100 px-3 text-sm outline-none placeholder:text-base-content/40 focus:border-primary";
const LABEL =
  "mb-1 block text-[10px] font-semibold uppercase tracking-wider text-base-content/50";

function Field({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={className}>
      <span className={LABEL}>{label}</span>
      {children}
    </div>
  );
}

type SetFn = (entries: Record<string, string | undefined>) => void;

// SkyWalking-style "Trace inspect" query panel, built from Avuru primitives.
// Reactive (apply on Enter / change) and URL-driven — no separate Run button.
// Inputs are keyed by their applied value so Clear (which empties the URL)
// remounts them blank.
export function TraceFilterPanel({
  filters,
  set,
  hasFilters,
  onClear,
}: {
  filters: TraceFilters;
  set: SetFn;
  hasFilters: boolean;
  onClear: () => void;
}) {
  const apply = (key: string) => (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") set({ [key]: (e.target as HTMLInputElement).value || undefined });
  };

  return (
    <Card className="p-3">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
        <Field label="Trace ID" className="col-span-2 xl:col-span-1">
          <input
            key={`trace-${filters.service ?? ""}`}
            type="text"
            placeholder="paste a trace id…"
            aria-label="Open trace by id"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                const v = (e.target as HTMLInputElement).value.trim();
                if (v) set({ trace: v });
              }
            }}
            className={`${INPUT} font-mono`}
          />
        </Field>

        <Field label="Service">
          <input
            key={`svc-${filters.service ?? ""}`}
            defaultValue={filters.service ?? ""}
            placeholder="any service"
            aria-label="Filter by service"
            onKeyDown={apply("service")}
            className={INPUT}
          />
        </Field>

        <Field label="Operation">
          <input
            key={`op-${filters.operation ?? ""}`}
            defaultValue={filters.operation ?? ""}
            placeholder="any operation"
            aria-label="Filter by operation"
            onKeyDown={apply("operation")}
            className={INPUT}
          />
        </Field>

        <Field label="Status">
          <select
            value={filters.status ?? ""}
            aria-label="Filter by status"
            onChange={(e) => set({ status: e.target.value || undefined })}
            className={INPUT}
          >
            <option value="">All</option>
            <option value="ok">OK</option>
            <option value="error">Error</option>
          </select>
        </Field>

        <Field label="Order">
          <select
            value={filters.order ?? ""}
            aria-label="Result order"
            onChange={(e) => set({ order: e.target.value || undefined })}
            className={INPUT}
          >
            <option value="">Newest</option>
            <option value="oldest">Oldest</option>
            <option value="slowest">Slowest</option>
          </select>
        </Field>

        <Field label="Duration (ms)">
          <div className="flex items-center gap-1.5">
            <input
              key={`min-${filters.minDurationMs ?? ""}`}
              type="number"
              min={0}
              defaultValue={filters.minDurationMs ?? ""}
              placeholder="min"
              aria-label="Minimum duration in ms"
              onKeyDown={apply("minMs")}
              className={INPUT}
            />
            <span className="text-base-content/40">–</span>
            <input
              key={`max-${filters.maxDurationMs ?? ""}`}
              type="number"
              min={0}
              defaultValue={filters.maxDurationMs ?? ""}
              placeholder="max"
              aria-label="Maximum duration in ms"
              onKeyDown={apply("maxMs")}
              className={INPUT}
            />
          </div>
        </Field>

        <Field label="Tags" className="col-span-2">
          <input
            key={`tags-${filters.tags ?? ""}`}
            defaultValue={filters.tags ?? ""}
            placeholder="http.status_code=500, http.method=GET"
            aria-label="Filter by span tags"
            onKeyDown={apply("tags")}
            className={`${INPUT} font-mono`}
          />
        </Field>
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-neutral/50 pt-3">
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
          <input
            type="checkbox"
            checked={Boolean(filters.includeAux)}
            onChange={(e) => set({ includeAux: e.target.checked ? "true" : undefined })}
            className="accent-primary"
          />
          Show auxiliary requests
          <span className="text-base-content/40">(health checks, /actuator, metrics, control-plane)</span>
        </label>
        {hasFilters && (
          <Button variant="ghost" size="sm" onClick={onClear}>
            <FilterX className="h-3.5 w-3.5" /> Clear
          </Button>
        )}
      </div>
    </Card>
  );
}
