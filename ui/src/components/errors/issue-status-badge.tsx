import { Badge } from "@/components/ui/badge";
import type { ErrorIssue } from "@/lib/api-types";

// One badge summarizing an issue's effective state. Regression wins the
// display: a resolved issue that came back is the thing that needs attention.
export function IssueStatusBadge({ issue }: { issue: Pick<ErrorIssue, "status" | "regressed"> }) {
  if (issue.regressed) {
    return <Badge tone="error">Regressed</Badge>;
  }
  switch (issue.status) {
    case "resolved":
      return <Badge tone="success">Resolved</Badge>;
    case "ignored":
      return <Badge tone="neutral">Ignored</Badge>;
    default:
      return <Badge tone="warning">Unresolved</Badge>;
  }
}
