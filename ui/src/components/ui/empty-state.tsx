import type { LucideIcon } from "lucide-react";

// Empty states teach (agent_docs/ui_patterns.md): always say what will
// appear here and what the user can do to make it appear.
export function EmptyState({
  icon: Icon,
  title,
  children,
}: {
  icon: LucideIcon;
  title: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-64 flex-col items-center justify-center gap-3 p-8 text-center">
      <div className="rounded-xl bg-base-300 p-3">
        <Icon className="h-6 w-6 text-primary" aria-hidden />
      </div>
      <h2 className="text-base font-semibold">{title}</h2>
      <div className="max-w-md text-sm text-base-content/60">{children}</div>
    </div>
  );
}
