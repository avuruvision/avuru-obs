"use client";

import { useState } from "react";
import { FilterX } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { Combobox } from "@/components/ui/combobox";
import { ApiError } from "@/lib/api";
import { TagChips } from "@/components/filters/tag-chips";
import { useResolveSpan, type TraceFilters } from "@/hooks/use-traces-data";

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
  services,
  servicesLoading,
  operations,
  operationsLoading,
  traceId,
  spanId,
}: {
  filters: TraceFilters;
  set: SetFn;
  hasFilters: boolean;
  onClear: () => void;
  services: string[];
  servicesLoading?: boolean;
  operations: string[];
  operationsLoading?: boolean;
  // The currently open trace/span (workspace URL params), so the ID input
  // reflects deep-links from Errors/Logs instead of staying blank.
  traceId?: string | null;
  spanId?: string | null;
}) {
  const apply = (key: string) => (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") set({ [key]: (e.target as HTMLInputElement).value || undefined });
  };

  const resolveSpan = useResolveSpan();
  const [idError, setIdError] = useState<string | null>(null);

  // 16 hex chars = a span id, which must be resolved to its trace first;
  // anything else opens as a trace id directly (32 hex is the strict form,
  // but stay permissive like the previous input).
  const openId = (v: string) => {
    if (/^[0-9a-fA-F]{16}$/.test(v)) {
      resolveSpan.mutate(v, {
        onSuccess: (res) => set({ trace: res.traceId, span: v }),
        onError: (err) =>
          setIdError(
            err instanceof ApiError && err.status === 404
              ? "No span found with this id."
              : "Span lookup failed.",
          ),
      });
      return;
    }
    set({ trace: v });
  };

  return (
    <Card className="p-3">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
        <Field label="Trace / Span ID" className="col-span-2 xl:col-span-1">
          {/* Uncontrolled, so the key is what resets it: on a service change
              (a pasted id no longer belongs to the new scope) and on the open
              trace changing (a deep-link should show the id it landed on). */}
          <input
            key={`trace-${filters.service ?? ""}-${spanId ?? traceId ?? ""}`}
            type="text"
            defaultValue={spanId ?? traceId ?? ""}
            placeholder="paste a trace or span id…"
            aria-label="Open trace or span by id"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                const v = (e.target as HTMLInputElement).value.trim();
                if (v) openId(v);
              }
            }}
            onChange={() => setIdError(null)}
            className={`${INPUT} font-mono`}
          />
          {idError && <p className="mt-1 text-xs text-error">{idError}</p>}
        </Field>

        <Field label="Service">
          <Combobox
            key={`svc-${filters.service ?? ""}`}
            value={filters.service ?? ""}
            options={services}
            loading={servicesLoading}
            onCommit={(v) => set({ service: v || undefined })}
            placeholder="any service"
            ariaLabel="Filter by service"
          />
        </Field>

        <Field label="Operation">
          <Combobox
            key={`op-${filters.operation ?? ""}`}
            value={filters.operation ?? ""}
            options={operations}
            loading={operationsLoading}
            onCommit={(v) => set({ operation: v || undefined })}
            placeholder="any operation"
            ariaLabel="Filter by operation"
          />
        </Field>

        <Field label="Status">
          <Select
            ariaLabel="Filter by status"
            value={filters.status ?? ""}
            onChange={(v) => set({ status: v || undefined })}
            options={[
              { value: "", label: "All" },
              { value: "ok", label: "OK" },
              { value: "refused", label: "Refused (4xx)" },
              { value: "error", label: "Error (5xx)" },
            ]}
          />
        </Field>

        <Field label="Order">
          <Select
            ariaLabel="Result order"
            value={filters.order ?? ""}
            onChange={(v) => set({ order: v || undefined })}
            options={[
              { value: "", label: "Newest" },
              { value: "oldest", label: "Oldest" },
              { value: "slowest", label: "Slowest" },
            ]}
          />
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

      {/* Business tags edit the same `tags` string as the field above — the
          chips are discovery, not a second filter. */}
      <TagChips
        value={filters.tags}
        onChange={(next) => set({ tags: next })}
        className="mt-2.5"
      />

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
