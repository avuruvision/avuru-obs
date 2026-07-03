"use client";

import { useMemo } from "react";
import { cn } from "@/lib/cn";
import { formatMs } from "@/lib/format";
import { serviceColor } from "@/lib/trace";
import { diffTraces, flattenDiff, type DiffNode } from "@/lib/trace-diff";
import type { TraceResponse } from "@/lib/api-types";

interface Change {
  label: string;
  cls: string;
  delta?: number; // ms, present only when the span exists in both
}

function classify(n: DiffNode): Change {
  if (n.aMs === undefined) return { label: "＋ added", cls: "text-success" };
  if (n.bMs === undefined) return { label: "－ removed", cls: "text-error" };
  const delta = n.bMs - n.aMs;
  // Significant only if it moved by >10% and >1ms.
  const significant = Math.abs(delta) > 1 && Math.abs(delta) > n.aMs * 0.1;
  if (!significant) return { label: "≈", cls: "text-base-content/50", delta };
  return delta > 0
    ? { label: "slower", cls: "text-error", delta }
    : { label: "faster", cls: "text-success", delta };
}

const signed = (ms: number) => (ms === 0 ? "0" : `${ms > 0 ? "+" : "−"}${formatMs(Math.abs(ms))}`);

// Structural diff overlay — Jaeger-style. Aligns two traces and marks each span
// added / removed / faster / slower.
export function TraceDiff({ a, b }: { a: TraceResponse; b: TraceResponse }) {
  const rows = useMemo(() => flattenDiff(diffTraces(a, b)), [a, b]);
  const maxDelta = useMemo(
    () =>
      Math.max(
        1,
        ...rows.map((n) =>
          n.aMs !== undefined && n.bMs !== undefined ? Math.abs(n.bMs - n.aMs) : 0,
        ),
      ),
    [rows],
  );

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-base-content/70">
        <span>
          <span className="font-mono text-base-content">A</span> {a.traceId.slice(0, 8)}… ·{" "}
          {formatMs(a.durationMs)}
        </span>
        <span>
          <span className="font-mono text-base-content">B</span> {b.traceId.slice(0, 8)}… ·{" "}
          {formatMs(b.durationMs)}
        </span>
        <span className="text-success">＋ added / faster</span>
        <span className="text-error">－ removed / slower</span>
      </div>
      <table className="table-dense w-full text-sm">
        <thead>
          <tr className="border-b border-neutral text-left text-base-content/60">
            <th>Span</th>
            <th className="text-right">A</th>
            <th className="text-right">B</th>
            <th className="text-right">Δ</th>
            <th className="w-40">change</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((n) => {
            const c = classify(n);
            return (
              <tr key={n.key} className="border-b border-neutral/40">
                <td>
                  <span
                    className="flex items-center gap-1.5"
                    style={{ paddingLeft: `${n.depth * 14}px` }}
                  >
                    <span
                      className="h-2.5 w-2.5 shrink-0 rounded-sm"
                      style={{ backgroundColor: serviceColor(n.service) }}
                      aria-hidden
                    />
                    <span className="truncate font-medium">{n.service}</span>
                    <span className="truncate font-mono text-xs text-base-content/55">
                      {n.operation}
                    </span>
                  </span>
                </td>
                <td className="text-right font-mono text-xs text-base-content/70">
                  {n.aMs === undefined ? "—" : formatMs(n.aMs)}
                </td>
                <td className="text-right font-mono text-xs text-base-content/70">
                  {n.bMs === undefined ? "—" : formatMs(n.bMs)}
                </td>
                <td className={cn("text-right font-mono text-xs", c.cls)}>
                  {c.delta === undefined ? "" : signed(c.delta)}
                </td>
                <td>
                  {c.delta !== undefined ? (
                    <span className="flex items-center gap-2">
                      <span className="h-1.5 flex-1 overflow-hidden rounded bg-base-300">
                        <span
                          className={cn(
                            "block h-full rounded",
                            c.delta > 0 ? "bg-error" : "bg-success",
                          )}
                          style={{
                            width: `${Math.min((Math.abs(c.delta) / maxDelta) * 100, 100)}%`,
                          }}
                        />
                      </span>
                      <span className={cn("w-12 text-right text-[10px]", c.cls)}>{c.label}</span>
                    </span>
                  ) : (
                    <span className={cn("text-[10px]", c.cls)}>{c.label}</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
