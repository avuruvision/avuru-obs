"use client";

import { Fragment, useMemo, useState } from "react";
import Link from "next/link";
import { ChevronDown, ChevronRight } from "lucide-react";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { formatShare } from "@/lib/format";
import { PostureBadge } from "./posture-badge";
import type { MeshWorkloadPosture } from "@/lib/api-types";

type SortKey = "namespace" | "name" | "mtlsShare" | "declaredMode" | "posture" | "callers";

// The share is lifted out of `observed` so the column can sort on it; an
// unmeasured row sorts as 0, which puts it beside the plaintext ones — the
// two things a reader of this table wants to look at first.
type Row = MeshWorkloadPosture & { mtlsShare?: number; callers: number };

const NAMESPACE: SortColumn<SortKey> = { key: "namespace", label: "Namespace" };
const NAME: SortColumn<SortKey> = { key: "name", label: "Workload" };
const OBSERVED: SortColumn<SortKey> = { key: "mtlsShare", label: "Observed mTLS", numeric: true };
const DECLARED: SortColumn<SortKey> = { key: "declaredMode", label: "Declared" };
const POSTURE: SortColumn<SortKey> = { key: "posture", label: "Posture" };
const CALLERS: SortColumn<SortKey> = { key: "callers", label: "Callers", numeric: true };

// One row per workload: what the cluster said, what the proxy saw, and the
// verdict. The Declared column exists only when the configuration half was
// read at all — a column of "default" on an install without the config module
// would claim a policy nobody looked up.
export function SecurityTable({
  rows,
  declared,
}: {
  rows: MeshWorkloadPosture[];
  declared: boolean;
}) {
  // Least encrypted first: the reason to open this tab is that something is
  // crossing the cluster in the clear.
  const sort = useColumnSort<SortKey>("mtlsShare", true);
  const [open, setOpen] = useState<Set<string>>(new Set());

  const sorted = useMemo(
    () =>
      sort.sortRows<Row>(
        rows.map((r) => ({
          ...r,
          mtlsShare: r.observed?.mtlsShare,
          callers: r.plaintextCallers?.length ?? 0,
        })),
      ),
    [rows, sort],
  );

  const toggle = (key: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const columns = declared ? 6 : 5;
  return (
    <div className="overflow-x-auto">
      <table data-testid="mesh-security-table" className="table-dense w-full text-sm">
        <thead className="text-xs text-base-content/55">
          <tr className="border-b border-neutral text-left">
            <SortableTh col={NAMESPACE} sort={sort} />
            <SortableTh col={NAME} sort={sort} />
            <SortableTh col={OBSERVED} sort={sort} iconFirst />
            {declared && <SortableTh col={DECLARED} sort={sort} />}
            <SortableTh col={POSTURE} sort={sort} />
            <SortableTh col={CALLERS} sort={sort} iconFirst />
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => {
            const key = `${r.namespace}/${r.name}`;
            const expanded = open.has(key);
            return (
              <Fragment key={key}>
                <tr className="border-b border-neutral/50 last:border-0">
                  <td className="text-base-content/70">{r.namespace}</td>
                  <td className="font-medium">
                    {/* The same page the map opens, when the workload has a
                        traced service behind it. A proxy-only row has none. */}
                    {r.service ? (
                      <Link
                        href={`/services?service=${encodeURIComponent(r.service)}`}
                        className="hover:text-primary hover:underline"
                      >
                        {r.name}
                      </Link>
                    ) : (
                      r.name
                    )}
                  </td>
                  <td className="text-right">
                    <ShareCell share={r.mtlsShare} />
                  </td>
                  {declared && <DeclaredCell row={r} />}
                  <td>
                    <PostureBadge posture={r.posture} />
                  </td>
                  <td className="text-right tabular-nums">
                    {r.callers > 0 ? (
                      <button
                        type="button"
                        onClick={() => toggle(key)}
                        aria-expanded={expanded}
                        className="inline-flex items-center gap-1 text-warning hover:underline"
                      >
                        {expanded ? (
                          <ChevronDown className="h-3 w-3" aria-hidden />
                        ) : (
                          <ChevronRight className="h-3 w-3" aria-hidden />
                        )}
                        {r.callers} plaintext {r.callers === 1 ? "caller" : "callers"}
                      </button>
                    ) : (
                      <span className="text-base-content/40">—</span>
                    )}
                  </td>
                </tr>
                {expanded && (
                  <tr className="border-b border-neutral/50 bg-base-300/20">
                    <td colSpan={columns} className="px-6 py-2">
                      <ul className="flex flex-col gap-0.5 text-xs">
                        {r.plaintextCallers?.map((c) => (
                          <li key={`${c.namespace}/${c.name}`} className="flex gap-3">
                            <span className="font-mono">
                              {c.namespace}/{c.name}
                            </span>
                            <span className="tabular-nums text-base-content/55">
                              {c.units.toLocaleString()} in the clear
                            </span>
                          </li>
                        ))}
                      </ul>
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// A bar and a number. Absent is "—" with the reason on hover: a proxy that
// classified nothing in this window has not said 0%, it has said nothing.
function ShareCell({ share }: { share?: number }) {
  if (share === undefined) {
    return (
      <span className="text-base-content/40" title="not measured in this window">
        —
      </span>
    );
  }
  const clear = share < 0.999;
  return (
    <span className="inline-flex items-center justify-end gap-2">
      <span className="h-1.5 w-16 overflow-hidden rounded-full bg-warning/30" aria-hidden>
        <span className="block h-full rounded-full bg-success" style={{ width: `${share * 100}%` }} />
      </span>
      <span className={`tabular-nums ${clear ? "text-warning" : ""}`}>{formatShare(share)}</span>
    </span>
  );
}

// The mode in force and where it came from. Absent means no policy applies
// and the mesh default governs — which the hub did not read and will not
// guess, so the cell says "default" rather than a mode.
function DeclaredCell({ row }: { row: MeshWorkloadPosture }) {
  if (!row.declaredMode) {
    return (
      <td className="text-base-content/40" title="No policy selects this workload; the mesh default governs">
        default
      </td>
    );
  }
  return (
    <td className={row.declaredMode === "DISABLE" ? "text-warning" : "text-base-content/70"}>
      {row.declaredMode}
      {row.declaredScope && (
        <span className="ml-1 text-xs text-base-content/40">{row.declaredScope}</span>
      )}
    </td>
  );
}
