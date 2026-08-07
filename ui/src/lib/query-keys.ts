// Query-key convention: [project, signal, scope, filters] — the project
// (tenant) LEADS every data key so switching projects can never serve
// another project's cached data (agent_docs/ui_patterns.md). Instance-global
// keys (status, systemStatus, projects) carry no project element.

export interface TimeParams {
  start: string;
  end: string;
}

export const queryKeys = {
  status: ["status"] as const,
  systemStatus: ["system", "status"] as const,
  projects: ["projects"] as const,
  capabilities: ["capabilities"] as const,
  services: (p: string, t: TimeParams, includeAux?: boolean) =>
    [p, "services", "list", { ...t, includeAux }] as const,
  serviceMap: (p: string, t: TimeParams, includeAux?: boolean) =>
    [p, "service-map", { ...t, includeAux }] as const,
  traceOverview: (p: string, t: TimeParams, service?: string, includeAux?: boolean) =>
    [p, "traces", "overview", { ...t, service, includeAux }] as const,
  traces: (
    p: string,
    t: TimeParams,
    filters: Record<string, string | number | boolean | undefined>,
  ) => [p, "traces", "search", { ...t, ...filters }] as const,
  trace: (p: string, traceId: string) => [p, "traces", "detail", traceId] as const,
  heatmap: (
    p: string,
    t: TimeParams,
    filters: Record<string, string | number | boolean | undefined>,
  ) => [p, "traces", "heatmap", { ...t, ...filters }] as const,
  red: (p: string, t: TimeParams, service?: string, includeAux?: boolean) =>
    [p, "metrics", "red", { ...t, service, includeAux }] as const,
  profiledServices: (p: string, t: TimeParams) => [p, "profiles", "services", t] as const,
  flamegraph: (p: string, t: TimeParams, service: string) =>
    [p, "profiles", "flamegraph", { ...t, service }] as const,
  infraNodes: (p: string, t: TimeParams) => [p, "infra", "nodes", t] as const,
  infraPods: (p: string, t: TimeParams, node?: string) =>
    [p, "infra", "pods", { ...t, node }] as const,
  agents: (p: string, windowSec?: number) => [p, "agents", { windowSec }] as const,
  logs: (p: string, t: TimeParams, filters: Record<string, string | number | undefined>) =>
    [p, "logs", "search", { ...t, ...filters }] as const,
  traceLogs: (p: string, traceId: string) => [p, "logs", "trace", traceId] as const,
  errorIssues: (
    p: string,
    t: TimeParams,
    filters: Record<string, string | undefined>,
  ) => [p, "errors", "issues", { ...t, ...filters }] as const,
  errorIssue: (p: string, fingerprint: string) =>
    [p, "errors", "issue", fingerprint] as const,
  errorIssueEvents: (p: string, fingerprint: string) =>
    [p, "errors", "events", fingerprint] as const,
  errorIssueHistogram: (p: string, fingerprint: string, t: TimeParams) =>
    [p, "errors", "histogram", fingerprint, t] as const,
  healthGroups: (p: string, t: TimeParams, includeAux?: boolean) =>
    [p, "health", "groups", { ...t, includeAux }] as const,
  // Group DEFINITIONS, as opposed to healthGroups (their current health).
  // Instance-global (no project element): a group is an install-level way of
  // slicing services, the same for every project.
  serviceGroups: ["service-groups"] as const,
  alerts: (p: string) => [p, "alerts", "list"] as const,
  alertRules: (p: string) => [p, "alerts", "rules"] as const,
  // Green (module green). Summary is windowed; budgets are always the current
  // UTC calendar month, so their key carries no time.
  greenSummary: (p: string, t: TimeParams) => [p, "green", "summary", t] as const,
  greenBudgets: (p: string) => [p, "green", "budgets"] as const,
  // Channels are instance-global delivery endpoints (no project element,
  // like systemStatus) — the notification payload carries the tenant.
  alertChannels: ["alerts", "channels"] as const,
  // Ingest keys are scoped to a project (they authenticate that project's
  // telemetry), so the project leads the key like any per-project data key.
  ingestKeys: (p: string) => [p, "ingest-keys"] as const,
  // The collection overlay drives the release-wide sensor DaemonSet, so it is
  // instance-global (no project element, like alertChannels).
  collectionOverlay: ["collection", "overlay"] as const,
};
