import { Lock, LockOpen, Unlock } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import type { MeshObservedMtls } from "@/lib/api-types";

// The three answers the mesh screen keeps giving — what mTLS was declared,
// whether a workload is actually in the mesh, what the wire measured — in one
// place, so a namespace row and a workload page cannot spell them differently.

// One PeerAuthentication mode as a lock. `own` is the scope of the row it sits
// on: a mode decided at a wider scope than the row's own is inherited, and the
// suffix says so, because a namespace showing STRICT from a mesh-wide policy
// and one that set it itself need different edits to change.
export function MtlsLock({
  mode,
  source,
  policy,
  own,
}: {
  mode?: string;
  source?: string;
  policy?: string;
  own: "namespace" | "workload";
}) {
  if (!mode) {
    // No policy reaches this row and the mesh default governs — which the hub
    // did not read, so the cell says "default" rather than guessing a mode.
    return (
      <span
        className="text-base-content/40"
        title="No policy applies; the mesh default governs, and it was not read"
      >
        default
      </span>
    );
  }
  const Glyph = mode === "STRICT" ? Lock : mode === "PERMISSIVE" ? LockOpen : Unlock;
  const tone =
    mode === "STRICT"
      ? "text-success"
      : mode === "PERMISSIVE"
        ? "text-warning"
        : mode === "DISABLE"
          ? "text-error"
          : "text-base-content/70";
  const inherited = !!source && source !== own;
  const title = policy ? `${source ?? "policy"} policy ${policy}` : undefined;
  return (
    <span className={cn("inline-flex items-center gap-1 whitespace-nowrap", tone)} title={title}>
      <Glyph className="h-3.5 w-3.5 shrink-0" aria-hidden />
      <span className="font-mono text-xs">{mode}</span>
      {inherited && (
        <span className="text-[10px] font-normal text-base-content/45">inherited</span>
      )}
    </span>
  );
}

// Whether a workload is actually in the mesh — from its pods, not its labels.
// The warning badge is the gap this whole surface exists to show: asked for,
// running, and neither injected nor captured.
export function EnrolmentBadge({
  declaredMode,
  dataplaneMode,
  injected,
  captured,
}: {
  declaredMode?: string;
  dataplaneMode?: string;
  injected: boolean;
  captured: boolean;
}) {
  if (captured || dataplaneMode === "ambient") {
    return (
      <Badge tone="primary" title="Captured by the node agent (ambient)">
        captured
      </Badge>
    );
  }
  if (injected || dataplaneMode === "sidecar") {
    return <Badge title="A sidecar is injected in its pods">sidecar</Badge>;
  }
  if (declaredMode) {
    return (
      <Badge
        tone="warning"
        title={`Labels ask for ${declaredMode}; no pod is injected or captured`}
      >
        declared, not enrolled
      </Badge>
    );
  }
  return (
    <span className="text-base-content/40" title="Not asked into the mesh">
      out of mesh
    </span>
  );
}

// What the wire measured. "Not measured" is not 0%: a dash with the reason,
// never a number that was not there.
export function ObservedMtls({ value, className }: { value?: MeshObservedMtls; className?: string }) {
  if (value?.mtlsShare === undefined) {
    return (
      <span className={cn("text-base-content/40", className)} title="Not measured in this window">
        —
      </span>
    );
  }
  const share = value.mtlsShare;
  const counted = `${value.mtls.toLocaleString()} mTLS, ${value.plaintext.toLocaleString()} plaintext, ${value.unknown.toLocaleString()} unknown`;
  return (
    <span
      className={cn(
        "tabular-nums",
        share >= 0.995 ? "text-success" : share > 0 ? "text-warning" : "text-error",
        className,
      )}
      title={value.reporter ? `${counted} — reported by ${value.reporter}` : counted}
    >
      {(share * 100).toFixed(share >= 0.995 || share === 0 ? 0 : 1)}%
    </span>
  );
}
