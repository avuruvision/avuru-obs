import { cn } from "@/lib/cn";
import { statusDotClass, statusLabel } from "@/lib/health-status";
import type { HealthMember } from "@/lib/api-types";

// A compact, clickable chip for one service in a group card. The dot tracks
// EFFECTIVE status (post-propagation); a ring marks the currently-selected
// member.
export function MemberChip({
  member,
  selected,
  onSelect,
}: {
  member: HealthMember;
  selected: boolean;
  onSelect: (service: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect(member.service)}
      title={`${member.service} — ${statusLabel(member.effectiveStatus)}: ${member.reason}`}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs",
        "hover:border-primary/60 hover:bg-base-300/40",
        selected && "border-primary ring-1 ring-primary",
      )}
    >
      <span className={cn("h-2 w-2 shrink-0 rounded-full", statusDotClass(member.effectiveStatus))} aria-hidden />
      <span className="max-w-40 truncate font-mono">{member.service}</span>
    </button>
  );
}
