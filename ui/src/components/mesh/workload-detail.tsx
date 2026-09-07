"use client";

import Link from "next/link";
import { useMemo } from "react";
import { ArrowLeft, Boxes, ExternalLink } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs } from "@/components/ui/tabs";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { useCapabilities } from "@/hooks/use-capabilities";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshWorkload } from "@/hooks/use-mesh-data";
import { formatRate } from "@/lib/format";
import { statusDotClass, statusLabel, statusTone } from "@/lib/health-status";
import type { MeshWorkload, MeshWorkloadDetail as Detail } from "@/lib/api-types";
import { FindingCard } from "./config-browser";
import { EnrolmentBadge, MtlsLock, ObservedMtls } from "./posture";
import { PostureBadge } from "./posture-badge";
import { SnapshotNotes, UnreadableState } from "./snapshot-notes";
import { Item, WorkloadOverview } from "./workload-overview";
import { WorkloadLogs } from "./workload-logs";

// The page's tabs, keyed in the URL as wltab. The mesh screen's own keys
// (view, wl, wlns, mode, proxy, q, ns, role) stay untouched.
type WorkloadTab = "overview" | "logs";

// One workload, whole: what the cluster says it is, what was declared for it,
// what was measured, and which policies decided that.
export function WorkloadDetail({
  namespace,
  name,
  onBack,
}: {
  namespace: string;
  name: string;
  onBack: () => void;
}) {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const { data: caps } = useCapabilities();
  const one = useMeshWorkload(time, true, namespace, name);
  // Logs are the logs module's; without it the tab does not exist, and a
  // deep link to it lands on the overview.
  const logsOn = caps?.modules.includes("logs") ?? false;
  const tab: WorkloadTab = logsOn && get("wltab") === "logs" ? "logs" : "overview";

  const back = (
    <button
      type="button"
      onClick={onBack}
      className="inline-flex items-center gap-1 text-xs text-base-content/60 hover:text-base-content"
    >
      <ArrowLeft className="h-3.5 w-3.5" aria-hidden />
      All workloads
    </button>
  );

  if (one.isLoading) return <CenteredSpinner />;
  if (one.isError || !one.data) {
    // A 404 from the hub: nothing by that name in the snapshot. A stale link,
    // or a workload that has since been removed.
    return (
      <div className="flex flex-col gap-3">
        {back}
        <EmptyState icon={Boxes} title={`No workload ${namespace}/${name} in the cluster`}>
          It may have been removed since this page was linked, or the snapshot
          has not caught up with it yet.
        </EmptyState>
      </div>
    );
  }
  const data = one.data;
  if (data.state !== "ok") return <UnreadableState state={data.state} reason={data.reason} />;
  if (!data.workload) {
    return (
      <div className="flex flex-col gap-3">
        {back}
        <EmptyState icon={Boxes} title="Pods could not be read">
          {data.checksSkipped ?? "A workload is a fact about pods, and the hub could not look at them."}
        </EmptyState>
      </div>
    );
  }
  const w = data.workload;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        {back}
        <h1 className="font-mono text-lg">{w.name}</h1>
        <Badge>{w.kind}</Badge>
        <span className="text-xs text-base-content/60">{w.namespace}</span>
        <EnrolmentBadge
          declaredMode={w.declaredMode}
          dataplaneMode={w.dataplaneMode}
          injected={w.injected}
          captured={w.captured}
        />
        {data.health && (
          <Badge tone={statusTone(data.health.status)} title={data.health.reason} data-testid="mesh-workload-health">
            <span className={`mr-1.5 inline-block h-1.5 w-1.5 rounded-full ${statusDotClass(data.health.status)}`} />
            {statusLabel(data.health.status)}
          </Badge>
        )}
        {/* Traces and errors are the same screens every other workload uses;
            the link takes the reader there. Logs are a tab here, because the
            proxies' lines about this workload are on no other screen. */}
        <Link
          href={`/services?service=${encodeURIComponent(w.name)}`}
          className="ml-auto inline-flex items-center gap-1 text-xs text-primary hover:underline"
        >
          Traces &amp; errors
          <ExternalLink className="h-3 w-3" aria-hidden />
        </Link>
      </div>

      <SnapshotNotes
        podsTruncated={data.podsTruncated}
        missingKinds={data.missingKinds}
        checksSkipped={data.checksSkipped}
      />

      <Tabs<WorkloadTab>
        items={[
          { value: "overview", label: "Overview" },
          ...(logsOn ? [{ value: "logs" as const, label: "Logs" }] : []),
        ]}
        value={tab}
        onChange={(v) => setMany({ wltab: v === "overview" ? undefined : v })}
      />

      {tab === "logs" ? (
        <WorkloadLogs
          namespace={w.namespace}
          name={w.name}
          waypoint={w.waypoint ? `${w.waypointNamespace ?? w.namespace}/${w.waypoint}` : undefined}
        />
      ) : (
        <>
          <div className="grid gap-3 lg:grid-cols-2">
            <IdentityCard w={w} data={data} />
            <DeclaredVsObserved w={w} />
          </div>

          <WorkloadOverview w={w} data={data} />

          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">Findings</h2>
            {data.findings.length === 0 ? (
              <p className="text-xs text-base-content/55">
                Nothing this product checks for is wrong with this workload.
              </p>
            ) : (
              <div data-testid="mesh-workload-findings" className="flex flex-col gap-2">
                {data.findings.map((f, i) => (
                  <FindingCard key={`${f.code}-${i}`} finding={f} />
                ))}
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function IdentityCard({ w, data }: { w: MeshWorkload; data: Detail }) {
  const nodes = useMemo(
    () => [...new Set(data.pods.map((p) => p.node).filter((n): n is string => !!n))].sort(),
    [data.pods],
  );
  return (
    <Card className="p-4">
      <h2 className="mb-3 text-sm font-medium">Identity and binding</h2>
      <dl className="grid grid-cols-2 gap-3 text-sm">
        <Item label="Service account" value={w.serviceAccount || "—"} mono />
        <Item
          label="Pods"
          value={`${w.runningPods}/${w.pods} running`}
          tone={w.pods > 0 && w.runningPods === 0 ? "warning" : undefined}
        />
        <div className="col-span-2">
          <dt className="text-xs text-base-content/55">Nodes</dt>
          <dd className="mt-0.5 font-mono text-xs">
            {nodes.length ? nodes.join(", ") : "—"}
            {data.podsTotal > data.podsShown && (
              <span className="ml-1 text-base-content/45">
                (from {data.podsShown} of {data.podsTotal} pods)
              </span>
            )}
          </dd>
        </div>
        {w.hasTraffic && w.ratePerSec !== undefined && (
          <Item label="Traffic" value={formatRate(w.ratePerSec)} />
        )}
      </dl>
    </Card>
  );
}

// Two columns, on purpose: the left is what the configuration asked for, the
// right is what the wire did. A gap between them is the finding no policy
// check can produce.
function DeclaredVsObserved({ w }: { w: MeshWorkload }) {
  const d = w.declaredMtls;
  return (
    <Card className="p-4" data-testid="mesh-workload-mtls">
      <h2 className="mb-3 text-sm font-medium">Declared vs observed</h2>
      <div className="grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs text-base-content/55">Declared</p>
          <div className="mt-1 text-base">
            <MtlsLock own="workload" mode={d?.mode} source={d?.source} policy={d?.policy} />
          </div>
          <p className="mt-1 text-xs text-base-content/55">
            {d ? (
              <>
                decided by the {d.source} policy <span className="font-mono">{d.policy}</span>
              </>
            ) : (
              "No policy reaches this workload; the mesh default governs, and it was not read."
            )}
          </p>
        </div>
        <div>
          <p className="text-xs text-base-content/55">Observed</p>
          <div className="mt-1 text-base">
            <ObservedMtls value={w.observedMtls} />
          </div>
          {w.posture && w.posture !== "unknown" && (
            <div className="mt-2">
              <PostureBadge posture={w.posture} />
            </div>
          )}
          <p className="mt-1 text-xs text-base-content/55">
            {w.observedMtls?.mtlsShare === undefined
              ? "Nothing measured what this workload's traffic actually travelled under in this window."
              : "share of this workload's requests that arrived under mutual TLS"}
          </p>
        </div>
      </div>
    </Card>
  );
}
