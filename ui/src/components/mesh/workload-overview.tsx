"use client";

import Link from "next/link";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { formatAgo, utcTooltip } from "@/lib/format";
import type { MeshConfigRef, MeshPolicyRef, MeshWorkload, MeshWorkloadDetail as Detail } from "@/lib/api-types";
import { FindingCard } from "./config-browser";
import { waypointName } from "./waypoint-serves";

// The cluster's own record of a workload, the way an operator reads it in
// the cluster: what it is and since when, what it is related to, its labels
// and annotations, its pods with the rollout each belongs to, and every
// piece of mesh configuration that names it — policies by label, routes by
// Service. Two lists, one section: a page that showed only the first called
// a routed workload unconfigured.
export function WorkloadOverview({ w, data }: { w: MeshWorkload; data: Detail }) {
  const labels = Object.entries(data.labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const annotations = Object.entries(data.annotations ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const mode = w.dataplaneMode ?? (w.declaredMode ? `${w.declaredMode} (declared, not enrolled)` : "none");
  const waypointProxy = w.waypoint ? waypointName(w.waypoint, w.waypointNamespace) : "";
  return (
    <div className="flex flex-col gap-3" data-testid="mesh-workload-overview">
      <div className="grid gap-3 lg:grid-cols-2">
        <Card className="p-4">
          <h2 className="mb-3 text-sm font-medium">Overview</h2>
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <Item
              label="Created"
              value={
                w.createdAt
                  ? `${formatAgo(w.createdAt)}${w.createdFrom === "pods" ? " (from its pods)" : ""}`
                  : "—"
              }
              title={w.createdAt ? utcTooltip(w.createdAt) : undefined}
            />
            <Item label="Type" value={w.kind} />
            <Item label="Version" value={w.version ?? "no version label"} mono={!!w.version} />
            <Item label="App" value={w.app ?? "no app label"} mono={!!w.app} />
            <Item label="Mode" value={mode} />
          </dl>
        </Card>
        <Card className="p-4">
          <h2 className="mb-3 text-sm font-medium">Related</h2>
          <dl className="grid grid-cols-1 gap-3 text-sm">
            <div>
              <dt className="text-xs text-base-content/55">Services</dt>
              <dd className="mt-0.5 flex flex-wrap gap-2 font-mono text-xs">
                {w.services.length === 0
                  ? "none select it"
                  : w.services.map((s) => {
                      const name = s.includes("/") ? s.slice(s.indexOf("/") + 1) : s;
                      return (
                        <Link
                          key={s}
                          href={`/services?service=${encodeURIComponent(name)}`}
                          className="hover:text-primary hover:underline"
                        >
                          {s}
                        </Link>
                      );
                    })}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-base-content/55">L7 waypoint</dt>
              <dd className="mt-0.5 font-mono text-xs">
                {w.waypoint ? (
                  <>
                    <Link
                      href={`/mesh?proxy=${encodeURIComponent(
                        w.waypointNamespace ? `${waypointProxy}.${w.waypointNamespace}` : waypointProxy,
                      )}`}
                      className="hover:text-primary hover:underline"
                    >
                      {w.waypoint}
                      {w.waypointNamespace ? ` in ${w.waypointNamespace}` : ""}
                    </Link>
                    {w.waypointSource ? (
                      <span className="ml-1 text-base-content/45">bound at {w.waypointSource} scope</span>
                    ) : null}
                  </>
                ) : (
                  "none"
                )}
              </dd>
            </div>
          </dl>
        </Card>
      </div>

      <Card className="p-4">
        <h2 className="mb-2 text-sm font-medium">Labels</h2>
        {labels.length === 0 ? (
          <p className="text-xs text-base-content/55">No labels.</p>
        ) : (
          <div className="flex flex-wrap gap-1.5" data-testid="mesh-workload-labels">
            {labels.map(([k, v]) => (
              <Badge key={k}>
                {k}={v}
              </Badge>
            ))}
          </div>
        )}
        <details className="mt-3">
          <summary className="cursor-pointer text-xs text-base-content/60">
            Annotations ({annotations.length}
            {data.annotationsCut ? ", list cut by the hub" : ""})
          </summary>
          {annotations.length === 0 ? (
            <p className="mt-1 text-xs text-base-content/55">No annotations on the controller.</p>
          ) : (
            <dl className="mt-2 grid gap-1 font-mono text-xs">
              {annotations.map(([k, v]) => (
                <div key={k} className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-2">
                  <dt className="truncate text-base-content/60" title={k}>
                    {k}
                  </dt>
                  <dd className="break-all">{v}</dd>
                </div>
              ))}
            </dl>
          )}
        </details>
      </Card>

      <Card className="overflow-hidden">
        <div className="flex items-baseline justify-between px-4 pt-4">
          <h2 className="text-sm font-medium">Pods</h2>
          <span className="text-xs text-base-content/55">
            {data.podsTotal > data.podsShown ? `${data.podsShown} of ${data.podsTotal}` : data.podsTotal}
          </span>
        </div>
        {data.pods.length === 0 ? (
          <p className="px-4 pb-4 pt-2 text-xs text-base-content/55">No pod of this workload is in the snapshot.</p>
        ) : (
          <table className="table-dense mt-2 w-full text-sm" data-testid="mesh-workload-pods">
            <thead>
              <tr className="border-b border-neutral text-left">
                <th>Name</th>
                <th>Revision</th>
                <th>Phase</th>
                <th>Mesh</th>
                <th>Node</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {data.pods.map((p) => (
                <tr key={p.name} className="border-b border-neutral/40 last:border-0">
                  <td className="font-mono text-xs">{p.name}</td>
                  <td className="font-mono text-xs">{p.revision ?? "—"}</td>
                  <td>
                    <Badge tone={p.phase === "Running" ? "success" : "warning"}>{p.phase ?? "unknown"}</Badge>
                  </td>
                  <td className="text-xs">
                    {p.captured ? "captured" : p.injected ? "sidecar" : "not enrolled"}
                  </td>
                  <td className="font-mono text-xs">{p.node ?? "—"}</td>
                  <td className="text-xs" title={p.createdAt ? utcTooltip(p.createdAt) : undefined}>
                    {p.createdAt ? formatAgo(p.createdAt) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      <section className="flex flex-col gap-2" data-testid="mesh-workload-istio-config">
        <div>
          <h2 className="text-sm font-medium">Istio config</h2>
          <p className="mt-0.5 text-xs text-base-content/55">
            Every policy whose selector, namespace or mesh-wide scope reaches this
            workload, and every route or rule that reaches it through one of its
            Services — with what is wrong with each.
          </p>
        </div>
        {w.policies.length === 0 && data.routes.length === 0 ? (
          <p className="text-xs text-base-content/55">
            No policy or route names this workload; the mesh default governs it.
          </p>
        ) : (
          <>
            <div data-testid="mesh-workload-policies" className="flex flex-col gap-2">
              {w.policies.map((p) => (
                <ConfigRefRow
                  key={`${p.kind}/${p.namespace}/${p.name}`}
                  kind={p.kind}
                  namespace={p.namespace}
                  name={p.name}
                  note={`${p.scope} scope`}
                  findings={p.findings}
                />
              ))}
            </div>
            <div data-testid="mesh-workload-routes" className="flex flex-col gap-2">
              {data.routes.map((r) => (
                <ConfigRefRow
                  key={`${r.kind}/${r.namespace}/${r.name}/${r.service}`}
                  kind={r.kind}
                  namespace={r.namespace}
                  name={r.name}
                  note={`via ${r.service} · ${r.host}`}
                  findings={r.findings}
                />
              ))}
            </div>
          </>
        )}
      </section>
    </div>
  );
}

// One reference into the configuration browser, which encodes its selection
// as kind/namespace/name. Policies and routes share the row: a policy says at
// what scope it reached the workload, a route through which Service.
export function ConfigRefRow({
  kind,
  namespace,
  name,
  note,
  findings,
}: Pick<MeshPolicyRef, "kind" | "namespace" | "name"> & {
  note: string;
  findings?: MeshConfigRef["findings"];
}) {
  const qs = new URLSearchParams({
    view: "config",
    kind,
    cfgns: namespace,
    object: `${kind}/${namespace}/${name}`,
  });
  return (
    <Card className="p-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge>{kind}</Badge>
        <Link href={`/mesh?${qs}`} className="font-mono hover:text-primary hover:underline">
          {namespace}/{name}
        </Link>
        <span className="text-xs text-base-content/50">{note}</span>
      </div>
      {findings?.length ? (
        <div className="mt-2 flex flex-col gap-2">
          {findings.map((f, i) => (
            <FindingCard key={`${f.code}-${i}`} finding={f} />
          ))}
        </div>
      ) : null}
    </Card>
  );
}

export function Item({
  label,
  value,
  mono,
  tone,
  title,
}: {
  label: string;
  value: string;
  mono?: boolean;
  tone?: "warning";
  title?: string;
}) {
  return (
    <div>
      <dt className="text-xs text-base-content/55">{label}</dt>
      <dd
        title={title}
        className={`mt-0.5 ${mono ? "font-mono text-xs" : ""} ${tone === "warning" ? "text-warning" : ""}`}
      >
        {value}
      </dd>
    </div>
  );
}
