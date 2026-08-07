"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import type { ServiceGroupDef, ServiceGroupInput } from "@/lib/api-types";

const TIERS = [
  { value: "T0", label: "T0 — critical" },
  { value: "T1", label: "T1" },
  { value: "T2", label: "T2" },
  { value: "T3", label: "T3 — degradable" },
];

// Selectors are edited as one-per-line text rather than tag chips: an operator
// pasting a namespace list out of kubectl is the common case, and the hub
// trims and de-duplicates what arrives anyway.
function toLines(v: string[]): string {
  return v.join("\n");
}

function fromLines(v: string): string[] {
  return v
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

// Inline create/edit form for a service health group. The name is the identity
// (disabled on edit, as with alert channels); a group needs at least one
// namespace or service or it would match nothing.
export function ServiceGroupForm({
  initial,
  onSubmit,
  onCancel,
  busy,
}: {
  initial?: ServiceGroupDef;
  onSubmit: (input: ServiceGroupInput) => Promise<unknown>;
  onCancel: () => void;
  busy: boolean;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [tier, setTier] = useState(initial?.tier ?? "T2");
  const [namespaces, setNamespaces] = useState(toLines(initial?.namespaces ?? []));
  const [services, setServices] = useState(toLines(initial?.services ?? []));
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const input: ServiceGroupInput = {
      name: name.trim(),
      tier,
      namespaces: fromLines(namespaces),
      services: fromLines(services),
    };
    if (!input.namespaces.length && !input.services.length) {
      setError("Add at least one namespace or service — an empty group matches nothing.");
      return;
    }
    try {
      await onSubmit(input);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
    }
  };

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";
  const areaClass =
    "min-h-16 w-full rounded-lg border border-neutral bg-base-100 px-2.5 py-1.5 font-mono text-xs focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <form
      onSubmit={submit}
      className="flex flex-col gap-2 border-t border-neutral px-4 py-3"
      data-testid="service-group-form"
    >
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Name
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="payments"
            disabled={!!initial}
            required
            data-testid="service-group-name"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Criticality tier
          <Select
            value={tier}
            options={TIERS}
            onChange={setTier}
            ariaLabel="Criticality tier"
            className="h-8"
          />
        </label>
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Namespaces <span className="text-base-content/40">(one per line)</span>
          <textarea
            className={areaClass}
            value={namespaces}
            onChange={(e) => setNamespaces(e.target.value)}
            placeholder={"payments\nbilling"}
            data-testid="service-group-namespaces"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Services <span className="text-base-content/40">(one per line)</span>
          <textarea
            className={areaClass}
            value={services}
            onChange={(e) => setServices(e.target.value)}
            placeholder={"checkout\ncart"}
            data-testid="service-group-services"
          />
        </label>
      </div>
      {error && (
        <p className="text-xs text-error" data-testid="service-group-error">
          {error}
        </p>
      )}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy} data-testid="service-group-save">
          {initial ? "Save" : "Add group"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
