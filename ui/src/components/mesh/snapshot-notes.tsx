import { Network } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";

// What the cluster read could not give, in words — shared by every mesh-config
// surface so the namespaces tab, the workloads tab and a workload's page say
// the same thing about the same snapshot.

// A snapshot that was read, with holes. Nothing renders when there are none.
export function SnapshotNotes({
  truncated,
  podsTruncated,
  missingKinds,
  checksSkipped,
}: {
  truncated?: boolean;
  podsTruncated?: boolean;
  missingKinds?: string[];
  checksSkipped?: string;
}) {
  if (!truncated && !podsTruncated && !missingKinds?.length && !checksSkipped) return null;
  return (
    <p className="text-xs text-base-content/55">
      {truncated && "This cluster is larger than one snapshot; the list is cut short. "}
      {/* The pod cut has its own sentence from the hub, which says what was not
          checked as a result; the flag alone is repeated only when it does not. */}
      {podsTruncated && !checksSkipped && "The pod list was cut short; enrolment past the cut is unknown. "}
      {checksSkipped && <span className="text-warning">{sentence(checksSkipped)} </span>}
      {missingKinds?.length ? `Not readable here: ${missingKinds.join(", ")}.` : null}
    </p>
  );
}

// A snapshot that was not read at all. The title names the state, the body is
// the hub's own reason — which, for a forbidden read, names the grant to add.
export function UnreadableState({ state, reason }: { state: string; reason?: string }) {
  return (
    <EmptyState icon={Network} title={unreadableTitle(state)}>
      {reason ?? "The cluster's mesh configuration could not be read."}
    </EmptyState>
  );
}

export function unreadableTitle(state: string): string {
  switch (state) {
    case "forbidden":
      return "Not allowed to read the cluster";
    case "no-crds":
      return "No mesh configuration in this cluster";
    default:
      return "Cluster configuration not read";
  }
}

// The hub writes its reasons lower-case, to be embedded; on their own line they
// start a sentence.
function sentence(s: string): string {
  const t = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(t) ? t : `${t}.`;
}
