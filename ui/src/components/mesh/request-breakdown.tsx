"use client";

import { Card } from "@/components/ui/card";
import type { MeshWorkloadRequests } from "@/lib/api-types";

// A workload's requests as its proxy counted them: by response flag, with the
// proxy's own reason in words, and by the destination version each landed on.
// Neither dimension is in any application span — a circuit breaker opening
// looks, from the caller's trace, like any other refused request.
//
// The upstream-stats hint is rendered even when everything else is healthy:
// the per-upstream counters a reader looks for next are not collected by a
// default mesh, and a zero that means "not looking" is the lie this whole
// screen exists to avoid.
export function RequestBreakdown({
  data,
  loading,
}: {
  data?: MeshWorkloadRequests;
  loading: boolean;
}) {
  if (loading || !data) return null;
  return (
    <section data-testid="mesh-requests" className="flex flex-col gap-2">
      <div>
        <h2 className="text-sm font-medium">Requests by outcome</h2>
        <p className="mt-0.5 text-xs text-base-content/55">
          As this workload&apos;s proxy counted them — the dimensions no
          application span carries.
        </p>
      </div>
      {!data.measured ? (
        // No table: a table of zeros here would say "no failures" about a
        // workload nobody measured.
        <p className="text-xs text-base-content/60">
          {data.reason ?? "No data-plane series for this workload in this window."}
        </p>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          <FlagsCard data={data} />
          <VersionsCard data={data} />
        </div>
      )}
    </section>
  );
}

// UO is the one flag that names a decision the mesh made on its own — the
// circuit breaker is open, so the proxy refused before asking the upstream —
// which is why its row is tinted where the others are not.
function FlagsCard({ data }: { data: MeshWorkloadRequests }) {
  return (
    <Card className="overflow-hidden">
      <div className="border-b border-neutral px-4 py-2 text-xs text-base-content/55">
        Response flags
        {data.reporter && <span className="ml-1.5 text-base-content/40">reported by {data.reporter}</span>}
      </div>
      {data.responseFlags.length === 0 ? (
        <p className="px-4 py-3 text-xs text-base-content/55">
          No requests counted in this window.
        </p>
      ) : (
        <table data-testid="mesh-response-flags" className="table-dense w-full text-sm">
          <thead className="text-xs text-base-content/55">
            <tr className="border-b border-neutral text-left">
              <th>Flag</th>
              <th>Meaning</th>
              <th className="text-right">Requests</th>
            </tr>
          </thead>
          <tbody>
            {data.responseFlags.map((f) => (
              <tr
                key={f.flag}
                className={`border-b border-neutral/50 last:border-0 ${f.flag === "UO" ? "text-warning" : ""}`}
              >
                <td className="font-mono">{f.flag}</td>
                <td>
                  {f.meaning ?? (
                    <span className="text-base-content/40">not known to this product</span>
                  )}
                </td>
                <td className="text-right tabular-nums">{f.requests.toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
        {data.upstreamStatsHint}
      </p>
    </Card>
  );
}

function VersionsCard({ data }: { data: MeshWorkloadRequests }) {
  return (
    <Card className="overflow-hidden">
      <div className="border-b border-neutral px-4 py-2 text-xs text-base-content/55">
        By destination version
      </div>
      {data.destinationVersions.length === 0 ? (
        <p className="px-4 py-3 text-xs text-base-content/55">
          No destination version reported — the pods carry no version label.
        </p>
      ) : (
        <table data-testid="mesh-destination-versions" className="table-dense w-full text-sm">
          <thead className="text-xs text-base-content/55">
            <tr className="border-b border-neutral text-left">
              <th>Version</th>
              <th className="text-right">Requests</th>
            </tr>
          </thead>
          <tbody>
            {data.destinationVersions.map((v) => (
              <tr key={v.version} className="border-b border-neutral/50 last:border-0">
                <td className="font-mono">{v.version}</td>
                <td className="text-right tabular-nums">{v.requests.toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}
