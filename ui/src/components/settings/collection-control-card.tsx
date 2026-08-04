"use client";

import { useState } from "react";
import { X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ApiError } from "@/lib/api";
import { formatAgo } from "@/lib/format";
import {
  useCollectionOverlay,
  useResetCollectionOverlay,
  useSaveCollectionOverlay,
} from "@/hooks/use-collection-overlay";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import type { CollectionEffective, CollectionOverlay, ModuleName } from "@/lib/api-types";

type SignalKey = keyof Omit<CollectionOverlay, "excludeNamespaces">;

const SIGNAL_ROWS: {
  key: SignalKey;
  eff: keyof Omit<CollectionEffective, "excludeNamespaces">;
  label: string;
  detail: string;
  // Module whose install-time flag gates the signal one-way: the overlay can
  // only drive the sensor-side knob, never re-enable a module that is off
  // (hub/internal/collection/effective.go). null = core, never gated.
  module: ModuleName | null;
  moduleFlag: string;
}[] = [
  {
    key: "obiEnabled",
    eff: "obi",
    label: "Traces (OBI)",
    detail: "eBPF spans + service map edges",
    module: null,
    moduleFlag: "",
  },
  {
    key: "logsEnabled",
    eff: "logs",
    label: "Logs",
    detail: "container stdout/stderr via the otel-agent",
    module: "logs",
    moduleFlag: "modules.logs.enabled",
  },
  {
    key: "kubeletstatsEnabled",
    eff: "kubeletstats",
    label: "Pod metrics",
    detail: "kubeletstats CPU/memory per pod",
    module: "infra-metrics",
    moduleFlag: "modules.infraMetrics.enabled",
  },
  {
    key: "profilerEnabled",
    eff: "profiler",
    label: "Profiles",
    detail: "continuous CPU profiling",
    module: "profiling",
    moduleFlag: "modules.profiling.enabled",
  },
  {
    key: "greenEnabled",
    eff: "green",
    label: "Energy",
    detail: "per-node energy & carbon",
    module: "green",
    moduleFlag: "modules.green.enabled",
  },
];

// Kubernetes namespace grammar (RFC 1123 label), mirroring the hub's
// validation so a typo errors inline instead of as a 400 after Apply.
const NS_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

function sameOverlay(a: CollectionOverlay, b: CollectionOverlay): boolean {
  const bools: SignalKey[] = [
    "obiEnabled",
    "logsEnabled",
    "kubeletstatsEnabled",
    "profilerEnabled",
    "greenEnabled",
  ];
  if (bools.some((k) => (a[k] ?? null) !== (b[k] ?? null))) return false;
  const an = a.excludeNamespaces ?? null;
  const bn = b.excludeNamespaces ?? null;
  if (an === null || bn === null) return an === bn;
  return an.length === bn.length && an.every((v, i) => v === bn[i]);
}

function errText(err: unknown): string {
  return err instanceof ApiError ? err.message : "request failed";
}

// The writable half of Settings → Collection (design/
// 2026-07-27-collection-control-plane.md): five signal switches and the
// namespace-exclude list, edited as a local draft and applied in one PUT —
// each apply is one forced sensor rollout, so switches must not save on
// every flip. "Reset to chart defaults" is the explicit DELETE.
export function CollectionControlCard() {
  const { data, isLoading, isError } = useCollectionOverlay(true);
  const save = useSaveCollectionOverlay();
  const reset = useResetCollectionOverlay();

  // null = mirror the saved overlay; an object = the admin's pending edits.
  const [draft, setDraft] = useState<CollectionOverlay | null>(null);
  const [nsInput, setNsInput] = useState("");
  const [nsError, setNsError] = useState<string | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);

  const moduleOn: Record<string, boolean> = {
    logs: useModuleEnabled("logs"),
    "infra-metrics": useModuleEnabled("infra-metrics"),
    profiling: useModuleEnabled("profiling"),
    green: useModuleEnabled("green"),
  };

  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Collection control</CardTitle>
        </CardHeader>
        <div className="border-t border-neutral p-6">
          <CenteredSpinner />
        </div>
      </Card>
    );
  }
  if (isError || !data) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Collection control</CardTitle>
        </CardHeader>
        <p className="border-t border-neutral p-4 text-sm text-error">
          Couldn’t load the collection overlay from the hub.
        </p>
      </Card>
    );
  }

  const saved = data.overlay;
  const overlay = draft ?? saved;
  const effective = data.effective;
  const dirty = draft !== null && !sameOverlay(draft, saved);
  const busy = save.isPending || reset.isPending;
  // Editing needs the resolved base state to be truthful: without it a
  // switch would show "off" for a signal that is collecting fine.
  const editable = effective !== undefined && !busy;

  const edit = (patch: CollectionOverlay) => {
    setConfirmReset(false);
    setDraft({ ...(draft ?? saved), ...patch });
  };

  const setSignal = (key: SignalKey, value: boolean) => {
    const next = { ...(draft ?? saved) };
    next[key] = value;
    setConfirmReset(false);
    setDraft(next);
  };

  const namespaces = overlay.excludeNamespaces ?? effective?.excludeNamespaces ?? [];

  const addNamespace = () => {
    const ns = nsInput.trim();
    if (ns === "") return;
    if (ns.length > 63 || !NS_RE.test(ns)) {
      setNsError(`"${ns}" is not a valid namespace name`);
      return;
    }
    if (namespaces.includes(ns)) {
      setNsError(`"${ns}" is already excluded`);
      return;
    }
    setNsError(null);
    setNsInput("");
    edit({ excludeNamespaces: [...namespaces, ns] });
  };

  const apply = () => {
    if (draft === null) return;
    save.mutate(draft, { onSuccess: () => setDraft(null) });
  };

  const doReset = () => {
    if (!confirmReset) {
      setConfirmReset(true);
      return;
    }
    setConfirmReset(false);
    reset.mutate(undefined, { onSuccess: () => setDraft(null) });
  };

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Collection control</CardTitle>
        {data.updatedBy && (
          <span className="text-xs text-base-content/50">
            changed by {data.updatedBy}
            {data.updatedAt ? ` · ${formatAgo(data.updatedAt)}` : ""}
          </span>
        )}
      </CardHeader>

      {effective === undefined && (
        <p className="border-t border-neutral bg-warning/10 px-4 py-2.5 text-xs text-warning">
          The hub couldn’t resolve the live collection state from the cluster,
          so the switches are read-only. Saved overrides still persist; check
          the hub logs if this persists.
        </p>
      )}

      <ul className="divide-y divide-neutral border-t border-neutral">
        {SIGNAL_ROWS.map(({ key, eff, label, detail, module, moduleFlag }) => {
          const gatedOff = module !== null && !moduleOn[module];
          const checked = !gatedOff && (overlay[key] ?? effective?.[eff] ?? false);
          return (
            <li key={key} className="flex items-center justify-between gap-3 px-4 py-2.5">
              <span>
                <span className="block text-sm font-medium">{label}</span>
                <span className="block text-xs text-base-content/45">
                  {gatedOff ? (
                    <>
                      module off at install time — enable{" "}
                      <code className="font-mono">{moduleFlag}=true</code>
                    </>
                  ) : (
                    detail
                  )}
                </span>
              </span>
              <span className="flex items-center gap-2">
                {overlay[key] !== undefined && !gatedOff && (
                  <Badge tone="primary">overridden</Badge>
                )}
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-sm"
                  aria-label={label}
                  checked={checked}
                  disabled={!editable || gatedOff}
                  onChange={(e) => setSignal(key, e.target.checked)}
                />
              </span>
            </li>
          );
        })}
      </ul>

      <div className="flex flex-col gap-2 border-t border-neutral p-4">
        <span className="text-sm font-medium">Excluded namespaces</span>
        <span className="text-xs text-base-content/45">
          No signal is collected from these namespaces (all sensors honor the
          same list).
        </span>
        <div className="flex flex-wrap items-center gap-1.5">
          {namespaces.map((ns) => (
            <Badge key={ns} tone="neutral" className="gap-1 pr-0.5">
              {ns}
              <button
                type="button"
                aria-label={`Remove ${ns}`}
                className="rounded p-0.5 hover:bg-base-content/10"
                disabled={!editable}
                onClick={() =>
                  edit({ excludeNamespaces: namespaces.filter((n) => n !== ns) })
                }
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
          {namespaces.length === 0 && (
            <span className="text-xs text-base-content/45">
              none — every namespace is collected
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={nsInput}
            placeholder="namespace"
            aria-label="Namespace to exclude"
            disabled={!editable}
            onChange={(e) => {
              setNsInput(e.target.value);
              setNsError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addNamespace();
              }
            }}
            className="h-7 w-48 rounded-lg border border-neutral bg-base-200 px-2.5 font-mono text-xs focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:opacity-50"
          />
          <Button variant="secondary" size="sm" disabled={!editable} onClick={addNamespace}>
            Exclude
          </Button>
        </div>
        {nsError && <p className="text-xs text-error">{nsError}</p>}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t border-neutral p-4">
        <Button variant="primary" size="sm" disabled={!dirty || busy} onClick={apply}>
          {save.isPending ? "Applying…" : "Apply changes"}
        </Button>
        <Button
          variant={confirmReset ? "danger" : "ghost"}
          size="sm"
          disabled={busy}
          onClick={doReset}
        >
          {confirmReset ? "Confirm reset" : "Reset to chart defaults"}
        </Button>
        <span className="text-xs text-base-content/45">
          applying rolls out the sensor DaemonSet
        </span>
        {save.isError && (
          <p className="w-full text-xs text-error">{errText(save.error)}</p>
        )}
        {reset.isError && (
          <p className="w-full text-xs text-error">{errText(reset.error)}</p>
        )}
      </div>
    </Card>
  );
}
