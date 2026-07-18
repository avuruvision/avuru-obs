import type { HealthStatus } from "@/lib/api-types";

// Badge tone per health status. Deliberately maps idle → neutral (NOT warning):
// on the health board "idle" means "no traffic in this window", which must not
// read as a problem the way system-status.tsx renders idle amber.
export type StatusTone = "success" | "warning" | "error" | "neutral";

export function statusTone(status: HealthStatus | string): StatusTone {
  switch (status) {
    case "healthy":
      return "success";
    case "degraded":
      return "warning";
    case "down":
      return "error";
    default: // idle, unknown
      return "neutral";
  }
}

export function statusLabel(status: HealthStatus | string): string {
  switch (status) {
    case "healthy":
      return "Healthy";
    case "degraded":
      return "Degraded";
    case "down":
      return "Down";
    case "idle":
      return "Idle";
    default:
      return "Unknown";
  }
}

// A small colored dot for chips/headers — the fill tracks the badge tone.
export function statusDotClass(status: HealthStatus | string): string {
  switch (status) {
    case "healthy":
      return "bg-success";
    case "degraded":
      return "bg-warning";
    case "down":
      return "bg-error";
    default:
      return "bg-base-content/30";
  }
}
