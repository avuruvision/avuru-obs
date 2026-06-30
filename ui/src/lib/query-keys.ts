// Query-key convention: [signal, scope, filters] (agent_docs/ui_patterns.md).

export interface TimeParams {
  start: string;
  end: string;
}

export const queryKeys = {
  status: ["status"] as const,
  systemStatus: ["system", "status"] as const,
  services: (t: TimeParams) => ["services", "list", t] as const,
  serviceMap: (t: TimeParams, includeAux?: boolean) =>
    ["service-map", { ...t, includeAux }] as const,
  traceOverview: (t: TimeParams, service?: string, includeAux?: boolean) =>
    ["traces", "overview", { ...t, service, includeAux }] as const,
  traces: (t: TimeParams, filters: Record<string, string | number | boolean | undefined>) =>
    ["traces", "search", { ...t, ...filters }] as const,
  trace: (traceId: string) => ["traces", "detail", traceId] as const,
  heatmap: (t: TimeParams, filters: Record<string, string | number | boolean | undefined>) =>
    ["traces", "heatmap", { ...t, ...filters }] as const,
  logs: (t: TimeParams, filters: Record<string, string | number | undefined>) =>
    ["logs", "search", { ...t, ...filters }] as const,
  traceLogs: (traceId: string) => ["logs", "trace", traceId] as const,
};
