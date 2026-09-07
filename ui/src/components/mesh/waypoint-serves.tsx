"use client";

import Link from "next/link";
import { AlertTriangle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useMeshWaypointServes } from "@/hooks/use-mesh-data";

// What a waypoint serves, from the cluster's bindings.
//
// Its own traffic cannot answer this: a waypoint nothing is bound to and one
// whose clients are idle look the same on the wire. So the section reads the
// bindings, and says outright when there are none — or when the bindings are
// there and nothing runs the waypoint they name.
export function WaypointServes({ namespace, name }: { namespace: string; name: string }) {
  const serves = useMeshWaypointServes(true, namespace, name);
  const data = serves.data;
  const bound = data ? data.namespaces.length + data.services.length + data.workloads.length : 0;

  return (
    <section className="flex flex-col gap-2" data-testid="mesh-waypoint-serves">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-medium">Serves</h2>
        {data?.waypoint?.scope && (
          <Badge title="What this waypoint's Gateway says it serves">{data.waypoint.scope}</Badge>
        )}
      </div>
      {serves.isLoading ? (
        <CenteredSpinner />
      ) : serves.isError || !data ? (
        <p className="text-xs text-base-content/55">
          No waypoint {namespace}/{name} in the cluster snapshot: no Gateway of that
          name, and nothing bound to it.
        </p>
      ) : data.state !== "ok" ? (
        <p className="text-xs text-base-content/55">
          {data.reason ?? "The cluster's mesh configuration could not be read."}
        </p>
      ) : (
        <>
          {data.waypoint?.running === false && (
            <p className="inline-flex items-center gap-1 text-xs text-warning">
              <AlertTriangle className="h-3.5 w-3.5" aria-hidden />
              Nothing runs this waypoint — no Running pod serves its Gateway, so
              what is bound to it has no proxy.
            </p>
          )}
          {bound === 0 ? (
            <p className="text-xs text-base-content/55">Nothing is bound to this waypoint.</p>
          ) : (
            <div className="grid gap-3 sm:grid-cols-3">
              <BoundList title="Namespaces" items={data.namespaces} href={(ns) => workloadsIn(ns)} />
              <BoundList
                title="Services"
                items={data.services}
                href={(s) => workloadsIn(s.split("/")[0])}
              />
              <BoundList
                title="Workloads"
                items={data.workloads}
                href={(w) => `${workloadsIn(w.split("/")[0])}&wl=${encodeURIComponent(w)}`}
              />
            </div>
          )}
        </>
      )}
    </section>
  );
}

function workloadsIn(namespace: string): string {
  return `/mesh?view=workloads&wlns=${encodeURIComponent(namespace)}`;
}

function BoundList({
  title,
  items,
  href,
}: {
  title: string;
  items: string[];
  href: (item: string) => string;
}) {
  return (
    <div>
      <h3 className="text-xs text-base-content/55">
        {title} <span className="tabular-nums">({items.length})</span>
      </h3>
      {items.length === 0 ? (
        <p className="mt-1 text-xs text-base-content/40">—</p>
      ) : (
        <ul className="mt-1 flex flex-col gap-0.5 font-mono text-xs">
          {items.map((item) => (
            <li key={item}>
              <Link href={href(item)} className="hover:text-primary hover:underline">
                {item}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// The sensor names a waypoint's telemetry service "<gateway>.<namespace>"; the
// cluster names the Gateway without the suffix.
export function waypointName(proxyName: string, namespace?: string): string {
  if (namespace && proxyName.endsWith(`.${namespace}`)) {
    return proxyName.slice(0, -(namespace.length + 1));
  }
  return proxyName;
}
