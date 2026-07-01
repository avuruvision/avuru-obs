import { Badge } from "@/components/ui/badge";

// OTel severities → badge tone. ERROR/FATAL read as errors, WARN as warning,
// everything calmer stays neutral so the eye lands on what matters.
const TONE: Record<string, "neutral" | "warning" | "error"> = {
  TRACE: "neutral",
  DEBUG: "neutral",
  INFO: "neutral",
  WARN: "warning",
  WARNING: "warning",
  ERROR: "error",
  FATAL: "error",
};

export function SeverityBadge({ severity }: { severity: string }) {
  const tone = TONE[(severity ?? "").toUpperCase()] ?? "neutral";
  return <Badge tone={tone}>{severity || "—"}</Badge>;
}
