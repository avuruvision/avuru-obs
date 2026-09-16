import Link from "next/link";
import { Box, Crosshair, Server, X } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import { formatBytes, formatMs } from "@/lib/format";
import type { PodConnection, PodStats } from "@/lib/api-types";
import { podMatches } from "./model";

export function XRayInspector({ pod, selected, edges, isolated, visible, connectionsError, onIsolate, onClear }: {
  pod?: PodStats; selected?: string; edges: PodConnection[]; isolated: boolean; visible: boolean;
  connectionsError: boolean; onIsolate: () => void; onClear: () => void;
}) {
  if (!pod) return <aside aria-label="Pod inspector" className="flex flex-col justify-center gap-4 border-t border-neutral bg-base-200 p-6 xl:border-l xl:border-t-0">
    <Box className="h-9 w-9 text-primary" /><h2 className="text-xl font-medium">{selected ? "Pod outside this view" : "Look inside a workload."}</h2>
    <p className="text-sm text-base-content/70">{selected ? "The selected Pod is not in the current project, time window or filters." : "Select a Pod in the scene or use the Pod selector to inspect its placement, resources and observed connections."}</p>
    {selected && <Button onClick={onClear}>Clear selection</Button>}
  </aside>;
  const related = edges.filter(e => podMatches(pod, e.source) || podMatches(pod, e.target));
  const service = related.map(e => podMatches(pod, e.source) ? e.source.service : e.target.service).find(Boolean);
  return <aside aria-label="Pod inspector" data-testid="xray-inspector" className="flex min-w-0 flex-col gap-5 border-t border-neutral bg-base-200 p-5 xl:border-l xl:border-t-0">
    <div>
      <div className="flex items-center justify-between"><p className="text-[10px] tracking-widest text-base-content/60">SELECTED POD</p><Button size="icon" variant="ghost" aria-label="Clear Pod selection" onClick={onClear}><X className="h-4 w-4" /></Button></div>
      <Box className="my-3 h-9 w-9 rounded-lg border border-primary/40 bg-primary/10 p-1.5 text-primary" />
      <h2 className="break-all text-xl font-medium tracking-tight">{pod.name}</h2>
      <p className="mt-2 break-all text-xs text-base-content/65">{pod.namespace || "Namespace not reported"}</p>
      <span className="mt-3 inline-block rounded border border-neutral px-2 py-1 text-[10px]">Resource metrics observed</span>
    </div>
    <section className="border-t border-neutral pt-4">
      <h3 className="mb-3 text-[10px] tracking-widest text-base-content/60">PLACEMENT</h3>
      <p className="flex items-start gap-2 break-all text-xs"><Server className="h-4 w-4 shrink-0 text-primary" />{pod.node || "Node not reported"}</p>
      <p className="mt-3 break-all border-l border-neutral pl-5 text-xs text-base-content/70">{pod.workload || "Workload not reported"}</p>
    </section>
    <section className="border-t border-neutral pt-4">
      <h3 className="mb-4 text-[10px] tracking-widest text-base-content/60">RESOURCES</h3>
      <dl className="grid grid-cols-2 gap-4"><div><dt className="text-xs text-base-content/65">CPU</dt><dd className="mt-1 text-lg tabular-nums">{pod.cpuUsageCores.toFixed(3)} <span className="text-xs text-base-content/60">cores</span></dd></div><div><dt className="text-xs text-base-content/65">Memory</dt><dd className="mt-1 text-lg tabular-nums">{formatBytes(pod.memoryUsageBytes)}</dd></div></dl>
      <p className="mt-3 text-[11px] text-base-content/60">Kubernetes readiness and resource limits are not reported by this view.</p>
    </section>
    <section className="min-h-0 border-t border-neutral pt-4">
      <h3 className="mb-3 text-[10px] tracking-widest text-base-content/60">OBSERVED CONNECTIONS</h3>
      {connectionsError ? <p className="text-xs text-warning">Connections could not be loaded.</p> : related.length ? <ul className="max-h-48 space-y-3 overflow-y-auto">
        {related.map((edge, i) => {
          const outgoing = podMatches(pod, edge.source), ref = outgoing ? edge.target : edge.source;
          return <li key={i} className="text-xs"><p className="break-all">{outgoing ? "↗" : "↙"} {ref.name || edge.peer}{ref.namespace && <span className="text-base-content/55"> · {ref.namespace}</span>}</p>
            <p className="mt-1 text-[10px] text-base-content/65">{edge.calls.toLocaleString()} requests · p95 {formatMs(edge.p95Ms)}{edge.errors > 0 && <span className="text-error"> · {edge.errors} errors</span>}</p>
            {!ref.name && <p className="mt-1 text-[10px] text-base-content/60">Unresolved destination · location unknown</p>}
          </li>;
        })}
      </ul> : <p className="text-xs leading-relaxed text-base-content/65">No trace-backed connections for this Pod in this window. Pod identities must be present on the spans.</p>}
    </section>
    <div className="mt-auto space-y-3">
      {!visible && <p className="text-xs text-warning">This Pod is outside the scene limit. Narrow the node or Pod filter to bring it into view.</p>}
      <Button className="w-full" variant="primary" disabled={!visible} aria-pressed={isolated} onClick={onIsolate}><Crosshair className="h-4 w-4" />{isolated ? "Show all Pods" : "Isolate neighbourhood"}</Button>
      {service && <Link className={buttonVariants({ className: "w-full", size: "sm" })} href={`/traces?service=${encodeURIComponent(service)}`}>Explore service traces</Link>}
    </div>
  </aside>;
}
