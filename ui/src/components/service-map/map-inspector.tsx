"use client";

import Link from "next/link";
import { ArrowRight, Crosshair, Network, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { formatBytes, formatGco2e, formatMs, formatRate, formatWh } from "@/lib/format";
import { statusLabel, statusTone } from "@/lib/health-status";
import { virtualLabel } from "./graph-elements";

export function MapInspector({ service, selected, edges, health, carbon, logs, onSelect, onClear, onFocus }: {
  service?: ServiceStats;
  selected?: string;
  edges: ServiceEdge[];
  health?: ServiceHealth;
  carbon: boolean;
  logs: boolean;
  onSelect: (name: string) => void;
  onClear: () => void;
  onFocus: (name: string) => void;
}) {
  if (!service) return (
    <aside aria-label="Service inspector" className="flex min-h-64 flex-col justify-center gap-4 border-l border-neutral bg-base-200 p-6">
      <Network className="h-8 w-8 text-primary" aria-hidden />
      <h2 className="text-xl font-medium tracking-tight">{selected ? "Selection outside this view" : "Start with a connection."}</h2>
      <p className="text-sm leading-relaxed text-base-content/70">
        {selected ? `${selected} is not in the current result. Check your filters, project or time range.` : "Select a service on the map or use the service selector. See what calls it, what it depends on, and where to look next."}
      </p>
      {selected && <Button onClick={onClear}>Clear selection</Button>}
    </aside>
  );
  const inferred = service.role === "virtual";
  const peer = service.role === "peer";
  const observed = !inferred && !peer && service.spanCount > 0;
  const callers = edges.filter(e => e.target === service.name);
  const dependencies = edges.filter(e => e.source === service.name);
  const href = (path: string, name = service.name) => `${path}?service=${encodeURIComponent(name)}`;
  const related = (items: ServiceEdge[], upstream: boolean) => items.length ? (
    <ul className="space-y-2">
      {items.map((edge, index) => {
        const name = upstream ? edge.source : edge.target;
        return <li key={`${name}-${index}`} className="min-w-0 rounded-md border border-neutral p-2.5">
          <button onClick={() => onSelect(name)} className="max-w-full break-all text-left text-xs font-medium text-primary hover:underline">{virtualLabel(name)}</button>
          <p className="mt-1 text-[11px] text-base-content/70">
            {edge.calls > 0 ? `${edge.calls.toLocaleString()} calls · ${edge.p95Ms === undefined ? "latency not measured" : `${formatMs(edge.p95Ms)} caller p95`}` : `${edge.bytes === undefined ? "Volume not reported" : formatBytes(edge.bytes)} · network flow`}
          </p>
          {inferred && upstream && edge.calls > 0 && <Link href={href("/traces", name)} className="mt-2 inline-flex text-xs text-primary hover:underline">Inspect caller traces ↗</Link>}
        </li>;
      })}
    </ul>
  ) : <p className="text-xs text-base-content/75">None observed in this view.</p>;
  return (
    <aside aria-label="Service inspector" data-testid="map-inspector" className="flex min-w-0 flex-col gap-5 border-l border-neutral bg-base-200 p-5">
      <div>
        <div className="flex items-center justify-between gap-2">
          <p className="text-[10px] font-medium uppercase tracking-[0.16em] text-base-content/75">{inferred ? "Inferred dependency" : peer ? "Undetected peer" : "Selected service"}</p>
          <Button variant="ghost" size="icon" aria-label="Clear service selection" onClick={onClear}><X className="h-4 w-4" aria-hidden /></Button>
        </div>
        <h2 className="mt-2 break-all text-2xl font-medium tracking-tight">{virtualLabel(service.name)}</h2>
        <p className="mt-1 break-all text-xs text-base-content/75">{service.namespace || "Namespace not reported"}</p>
        <div className="mt-3"><Badge tone={inferred || peer ? "neutral" : statusTone(health?.status ?? "unknown")}>
          {inferred ? "Inferred" : peer ? "Not instrumented" : statusLabel(health?.status ?? "unknown")}
        </Badge></div>
        {health && !inferred && !peer && <p className="mt-2 text-xs leading-relaxed text-base-content/70">{health.reason}</p>}
      </div>
      <div className="border-y border-neutral py-4">
        <p className="text-[10px] uppercase tracking-wider text-base-content/75">Service p95 latency</p>
        <p data-testid="inspector-latency" className="mt-1 text-4xl font-medium tracking-tight">{observed ? formatMs(service.p95Ms) : "—"}</p>
        {observed ? <p className="mt-2 text-xs text-base-content/70">{formatRate(service.ratePerSec)} · {(service.errorRate * 100).toFixed(1)}% errors</p> : <p className="mt-2 text-xs leading-relaxed text-base-content/70">{inferred || peer ? "No own-service latency or health measurement. Connection data is observed from callers." : "No request measurements in this window."}</p>}
        {carbon && observed && <div className="mt-4 border-t border-neutral pt-3 text-xs">
          <p>{service.gco2e === undefined ? "Carbon not reported" : `${formatGco2e(service.gco2e)} CO₂e`} · {service.wh === undefined ? "Energy not reported" : formatWh(service.wh)}</p>
          <p className="mt-2 text-base-content/75">The map does not report measured/estimated provenance. Check the Green view before interpreting this value.</p>
          <Link href="/green" className="mt-2 inline-flex text-primary hover:underline">Energy methodology ↗</Link>
        </div>}
      </div>
      <section><h3 className="mb-2 text-xs font-semibold">Called by</h3>{related(callers, true)}</section>
      <section><h3 className="mb-2 text-xs font-semibold">Depends on</h3>{related(dependencies, false)}</section>
      <div className="mt-auto flex flex-col gap-2">
        <Button onClick={() => onFocus(service.name)}><Crosshair className="h-4 w-4" aria-hidden /> Focus neighbourhood</Button>
        {!inferred && !peer && <>
          <Link href={href("/services")} className={buttonVariants({variant:"primary"})}>Open service <ArrowRight className="h-4 w-4" aria-hidden /></Link>
          <div className="flex justify-between gap-3 text-xs text-primary">
            <Link href={href("/traces")} className="py-2 hover:underline">Explore traces ↗</Link>
            {logs && <Link href={href("/logs")} className="py-2 hover:underline">Read logs ↗</Link>}
          </div>
        </>}
      </div>
    </aside>
  );
}
