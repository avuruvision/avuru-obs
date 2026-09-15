import type { ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { virtualLabel } from "./graph-elements";

export function MapOverview({ services, health, healthEnabled, healthLoading, selected, onSelect }: {
  services: ServiceStats[]; health: Map<string, ServiceHealth>; healthEnabled: boolean;
  healthLoading: boolean; selected?: string; onSelect: (name: string) => void;
}) {
  const apps = services.filter(s => s.role !== "virtual" && s.role !== "peer" && s.role !== "transport");
  const dependencies = services.filter(s => s.role === "virtual");
  const observed = apps.filter(s => health.has(s.name));
  const attention = observed.filter(s => ["down", "degraded"].includes(health.get(s.name)!.status));
  return <>
    <div className="flex flex-wrap items-end justify-between gap-4 pb-3">
      <div><p className="text-[10px] uppercase tracking-[0.2em] text-base-content/65">Your connected system</p>
        <h1 className="mt-2 text-3xl font-medium sm:text-4xl">Nothing runs alone.</h1>
        <p className="mt-3 text-sm text-base-content/70">Follow a service. Keep its dependencies in view.</p></div>
      <label className="flex max-w-full flex-col gap-1.5 text-xs text-base-content/70">Inspect a service or dependency
        <select aria-label="Inspect a service or dependency" value={selected ?? ""} onChange={e => onSelect(e.target.value)}
          className="h-10 max-w-full rounded-md border border-neutral bg-base-200 px-3 text-sm text-base-content sm:w-64">
          <option value="">Select from the map…</option>
          {selected && !services.some(s => s.name === selected) && <option value={selected}>{selected} (outside this view)</option>}
          {services.map(s => <option key={s.name} value={s.name}>{virtualLabel(s.name)}{s.role === "virtual" ? " · inferred" : s.role === "peer" ? " · undetected" : ""}</option>)}
        </select>
      </label>
    </div>
    <div className="grid grid-cols-3 divide-x divide-neutral rounded-lg border border-neutral bg-base-200">
      <div className="p-3 sm:p-5"><p className="text-xs text-base-content/70">Applications in view</p><p className="mt-2 text-2xl font-medium">{apps.length}</p></div>
      <div className="p-3 sm:p-5"><p className="text-xs text-base-content/70">Inferred dependencies</p><p className="mt-2 text-2xl font-medium">{dependencies.length}</p></div>
      <div className="p-3 sm:p-5"><p className="text-xs text-base-content/70">Need attention</p><p className="mt-2 text-2xl font-medium">{!healthEnabled || healthLoading || !observed.length ? "—" : attention.length}</p><p className="mt-1 text-[10px] text-base-content/65">{!healthEnabled ? "Health module off" : healthLoading ? "Reading health…" : `${observed.length} of ${apps.length} with health data`}</p></div>
    </div>
    <p className="text-xs text-base-content/65">Select a node to inspect it. Use the selector for keyboard navigation.</p>
  </>;
}
