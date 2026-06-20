import type { LucideIcon } from "lucide-react";
import { EmptyState } from "./empty-state";
import { Badge } from "./badge";

export function ComingSoon({
  icon,
  title,
  milestone,
  children,
}: {
  icon: LucideIcon;
  title: string;
  milestone: string;
  children: React.ReactNode;
}) {
  return (
    <EmptyState icon={icon} title={title}>
      <p className="mb-2">{children}</p>
      <Badge tone="primary">arrives in {milestone}</Badge>
    </EmptyState>
  );
}
