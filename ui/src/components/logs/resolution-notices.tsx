import type { LogResolution } from "@/lib/api-types";

// How each subject's proxy lines were found, when a proxy source is on: no
// workload behind the name, matched by name rather than by pod, or compacted
// for pagination. One line per distinct notice, so two subjects that share a
// reason read once.
export function ResolutionNotices({ resolutions, sources }: { resolutions?: LogResolution[]; sources?: string }) {
  if (!sources?.split(",").some((source) => source === "ztunnel" || source === "waypoint")) return null;
  const notices = [...new Set((resolutions ?? []).flatMap((resolution) => {
    const subject = resolution.service || [resolution.namespace, resolution.workload].filter(Boolean).join("/");
    return [resolution.proxiesUnavailable, resolution.proxiesMatchedBy, resolution.proxiesFallback]
      .filter(Boolean)
      .map((message) => `${subject}: ${message}`);
  }))];
  if (notices.length === 0) return null;
  return (
    <div role="status" className="mb-3 rounded-lg border border-warning/50 bg-warning/10 px-3 py-2 text-xs text-base-content/75">
      {notices.map((notice) => <p key={notice}>{notice}</p>)}
    </div>
  );
}
