// Query-key convention: [signal, scope, filters] (agent_docs/ui_patterns.md).

export interface TimeParams {
  start: string;
  end: string;
}

export const queryKeys = {
  status: ["status"] as const,
  services: (t: TimeParams) => ["services", "list", t] as const,
  traceOverview: (t: TimeParams, service?: string) =>
    ["traces", "overview", { ...t, service }] as const,
  traces: (t: TimeParams, filters: Record<string, string | number | undefined>) =>
    ["traces", "search", { ...t, ...filters }] as const,
  trace: (traceId: string) => ["traces", "detail", traceId] as const,
  heatmap: (t: TimeParams, service?: string, operation?: string) =>
    ["traces", "heatmap", { ...t, service, operation }] as const,
  logs: (t: TimeParams, filters: Record<string, string | number | undefined>) =>
    ["logs", "search", { ...t, ...filters }] as const,
  traceLogs: (traceId: string) => ["logs", "trace", traceId] as const,
};
