import { Badge } from "@/components/ui/badge";

type Tone = "neutral" | "success" | "error" | "warning" | "info";

// How each verdict reads on screen. The hub sends the wire value; this is the
// only place that turns it into English and a colour, so the table, the facet
// and the findings cannot disagree about what "not enforced" looks like.
//
// The tones follow the finding they come with: the two errors are traffic
// crossing the cluster unprotected under a policy that says otherwise, the
// warning names callers to migrate, the info is the good news that STRICT
// would refuse nothing, and the success is the state everything else is
// trying to reach. Everything without a declared half is neutral — observed
// alone is a fact, not a verdict.
const POSTURES: Record<string, { label: string; tone: Tone }> = {
  "declared-strict-observed-plaintext": { label: "Not enforced", tone: "error" },
  uncarried: { label: "Not carried", tone: "error" },
  "permissive-with-plaintext-callers": { label: "Plaintext callers", tone: "warning" },
  "permissive-but-all-mtls": { label: "Safe to tighten", tone: "info" },
  "strict-and-mtls": { label: "Strict, all mTLS", tone: "success" },
  disabled: { label: "Disabled", tone: "neutral" },
  "observed-only-mtls": { label: "Observed only", tone: "neutral" },
  "observed-only-plaintext": { label: "Observed only", tone: "neutral" },
  idle: { label: "Idle", tone: "neutral" },
  unknown: { label: "Unknown", tone: "neutral" },
};

// A verdict this map does not know renders verbatim rather than as "Unknown":
// a hub newer than the UI is a normal state during a rolling upgrade, and the
// raw value tells the reader something true.
export function postureLabel(posture: string): string {
  return POSTURES[posture]?.label ?? posture;
}

export function PostureBadge({ posture }: { posture: string }) {
  const p = POSTURES[posture] ?? { label: posture, tone: "neutral" as Tone };
  return (
    <Badge tone={p.tone} title={posture}>
      {p.label}
    </Badge>
  );
}
