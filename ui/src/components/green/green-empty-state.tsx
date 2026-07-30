"use client";

import { Leaf } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { CopyButton } from "@/components/ui/copy-button";

// Energy is measured from the CPU's RAPL counters via Kepler — a hardware
// signal most public-cloud VMs don't expose. A green install with no data is
// therefore expected, not broken: this teaches the prerequisites instead of
// alarming. Empty states teach (agent_docs/ui_patterns.md).
const HELM_SNIPPET = [
  "helm upgrade … \\",
  "  --set modules.green.enabled=true \\",
  "  --set sensor.green.enabled=true",
].join("\n");

const ESTIMATION_HELM_SNIPPET = [
  "helm upgrade … \\",
  "  --set sensor.green.estimation.enabled=true",
].join("\n");

const CHECKLIST: { ok: string; detail: string }[] = [
  {
    ok: "Bare-metal or metal instances",
    detail:
      "RAPL is exposed on physical CPUs; most virtualized cloud VMs report nothing.",
  },
  {
    ok: "powercap readable in the kernel",
    detail: "Kepler reads /sys/class/powercap — check the node exposes it.",
  },
  {
    ok: "infra-metrics module on",
    detail: "The pod→workload join reads kubeletstats attributes; green requires it.",
  },
  {
    ok: "No RAPL? Enable TDP estimation instead",
    detail:
      "sensor.green.estimation.enabled models power from CPU utilization (±30-50% typical error), stamped estimated everywhere it appears.",
  },
];

export function GreenEmptyState() {
  return (
    <div className="flex flex-col gap-4">
      <EmptyState icon={Leaf} title="No energy measured yet">
        Per-service energy comes from your CPUs&apos; <strong>RAPL</strong> counters,
        read by the Kepler sensor container. On clusters without RAPL — most
        public-cloud VMs — there is simply nothing to measure by default, and
        that is expected; the last item below turns on a modeled estimate
        instead. Where RAPL is present, data appears within a collection interval.
      </EmptyState>

      <div className="mx-auto w-full max-w-xl rounded-xl border border-neutral bg-base-200 p-4 [box-shadow:var(--shadow-card)]">
        <h3 className="mb-3 text-sm font-semibold">Prerequisites</h3>
        <ul className="flex flex-col gap-2.5">
          {CHECKLIST.map((c) => (
            <li key={c.ok} className="flex items-start gap-2 text-sm">
              <span className="mt-0.5 text-success" aria-hidden>
                ✓
              </span>
              <span>
                <span className="font-medium">{c.ok}</span>
                <span className="block text-xs text-base-content/55">{c.detail}</span>
              </span>
            </li>
          ))}
        </ul>

        <div className="mt-4">
          <div className="mb-1.5 flex items-center justify-between">
            <span className="text-xs font-medium text-base-content/70">
              Enable the module and its sensor
            </span>
            <CopyButton value={HELM_SNIPPET} label="Copy" ariaLabel="Copy Helm command" />
          </div>
          <pre className="overflow-x-auto rounded-lg bg-base-300 px-3 py-2 text-xs leading-relaxed">
            {HELM_SNIPPET}
          </pre>
          <p className="mt-2 text-xs text-base-content/50">
            Both are off by default — the signal is hardware-dependent, so green
            never flips on for an existing install on upgrade.
          </p>
        </div>

        <div className="mt-4 border-t border-neutral/50 pt-4">
          <div className="mb-1.5 flex items-center justify-between">
            <span className="text-xs font-medium text-base-content/70">
              No RAPL? Estimate instead (requires the above)
            </span>
            <CopyButton
              value={ESTIMATION_HELM_SNIPPET}
              label="Copy"
              ariaLabel="Copy TDP estimation Helm command"
            />
          </div>
          <pre className="overflow-x-auto rounded-lg bg-base-300 px-3 py-2 text-xs leading-relaxed">
            {ESTIMATION_HELM_SNIPPET}
          </pre>
          <p className="mt-2 text-xs text-base-content/50">
            Models power from CPU utilization — every number it produces is
            stamped <span className="italic">estimated</span>, never blended
            with measured data.
          </p>
        </div>
      </div>
    </div>
  );
}
