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
};
