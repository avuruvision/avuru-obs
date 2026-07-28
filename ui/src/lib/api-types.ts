// Hand-written M1 mirror of the hub API DTOs (hub/internal/api/dto.go).
// Replaced by proto/buf codegen in a follow-up — keep field names in sync.

// Signal families this install runs. Mirrors the Go registry in
// hub/internal/modules — keep the wire names in sync. `core` (service map,
// traces, RED) is always present.
export type ModuleName =
  | "core"
  | "logs"
  | "infra-metrics"
  | "profiling"
  | "error-tracking"
  | "service-health"
  | "alerting";

export interface CapabilitiesResponse {
  version: string;
  modules: ModuleName[];
}

export interface ServiceStats {
  name: string;
  spanCount: number;
  ratePerSec: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface ServicesResponse {
  services: ServiceStats[];
}

export interface ServiceEdge {
  source: string;
  target: string;
  calls: number;
  errorCount: number;
  errorRate: number;
  bytes?: number; // network flow bytes (flow/both edges)
  provenance?: string; // "trace" | "flow" | "both"
  rttMs?: number; // OBI TCP RTT p95 (network-health edges)
  failedConnections?: number; // OBI failed/reset TCP connections
}

export interface ServiceMapResponse {
  services: ServiceStats[];
  edges: ServiceEdge[];
}

export interface OperationStats {
  service: string;
  operation: string;
  count: number;
  errorCount: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface OverviewResponse {
  operations: OperationStats[];
}

export interface TraceSummary {
  traceId: string;
  rootService: string;
  rootOperation: string;
  startTime: string;
  durationMs: number;
  spanCount: number;
  errorCount: number;
  statusCode: string;
}

export interface TracesResponse {
  traces: TraceSummary[];
  nextCursor?: string;
}

export interface SpanLookupResponse {
  traceId: string;
  spanId: string;
}

export interface SpanEvent {
  time: string;
  name: string;
  attributes?: Record<string, string>;
}

export interface Span {
  spanId: string;
  parentSpanId: string;
  service: string;
  operation: string;
  kind: string;
  scopeName?: string;
  scopeVersion?: string;
  startTime: string;
  durationMs: number;
  statusCode: string;
  statusMessage?: string;
  attributes?: Record<string, string>;
  resourceAttributes?: Record<string, string>;
  events?: SpanEvent[];
}

export interface TraceResponse {
  traceId: string;
  startTime: string;
  durationMs: number;
  spans: Span[];
}

export interface HeatmapCell {
  t: number;
  d: number;
  count: number;
  errorCount: number;
}

export interface HeatmapResponse {
  startTime: string;
  endTime: string;
  timeBucketSec: number;
  durationBoundsMs: number[];
  cells: HeatmapCell[];
}

export interface StatusResponse {
  service: string;
  version: string;
  status: string;
  clickhouse: string;
}

export type HealthStatus =
  | "healthy"
  | "degraded"
  | "down"
  | "idle"
  | "unknown";

export interface ComponentHealth {
  name: string;
  status: HealthStatus;
  detail?: string;
}

export interface SignalStats {
  signal: string;
  rows: number;
  bytes: number;
  compressedBytes: number;
  compression: number;
  oldest?: string;
  newest?: string;
  retentionDays: number;
}

export interface DiskStats {
  name: string;
  freeBytes: number;
  totalBytes: number;
}

export interface SystemStatusResponse {
  version: string;
  overall: "healthy" | "degraded" | "down";
  checkedAt: string;
  components: ComponentHealth[];
  signals: SignalStats[];
  disks: DiskStats[];
}

export interface LogRecord {
  timestamp: string;
  severity: string;
  service: string;
  body: string;
  traceId?: string;
  spanId?: string;
  attributes?: Record<string, string>;
}

export interface LogsResponse {
  logs: LogRecord[];
  nextCursor?: string;
}

export interface MetricPoint {
  time: string;
  value: number;
}

export interface NodeStats {
  name: string;
  cpuUsageCores: number;
  memoryUsageBytes: number;
  memoryAvailableBytes: number;
  networkRxBytesPerSec: number;
  networkTxBytesPerSec: number;
  podCount: number;
  cpuSeries: MetricPoint[];
  memorySeries: MetricPoint[];
}

export interface NodesResponse {
  nodes: NodeStats[];
}

export interface PodStats {
  name: string;
  namespace: string;
  node: string;
  workload?: string;
  cpuUsageCores: number;
  memoryUsageBytes: number;
}

export interface PodsResponse {
  pods: PodStats[];
}

export interface RedPoint {
  time: string;
  ratePerSec: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface RedSeries {
  service: string;
  points: RedPoint[];
}

export interface RedResponse {
  bucketSeconds: number;
  series: RedSeries[];
}

export interface ProfiledService {
  name: string;
  samples: number;
}

export interface ProfiledServicesResponse {
  services: ProfiledService[];
}

export interface FlameNode {
  name: string;
  value: number;
  self?: number;
  children?: FlameNode[];
}

export interface FlamegraphResponse {
  root: FlameNode;
}

export interface Project {
  id: string;
  source: "default" | "config" | "data";
}

export interface ProjectsResponse {
  projects: Project[];
}

export interface AgentSignals {
  traces: string | null;
  logs: string | null;
  metrics: string | null;
  profiles: string | null;
}

export interface AgentNode {
  node: string;
  lastSeen: string;
  signals: AgentSignals;
}

export interface AgentsResponse {
  sensors: AgentNode[];
  windowSeconds: number;
}

// Error tracking (module error-tracking). Mirrors hub/internal/api/error_tracking.go.
export type ErrorIssueStatus = "unresolved" | "resolved" | "ignored";

export interface ErrorIssue {
  fingerprint: string;
  service: string;
  type: string;
  message: string;
  source: string;
  status: ErrorIssueStatus;
  regressed: boolean;
  firstSeen: string;
  lastSeen: string;
  count: number;
  lastTraceId?: string;
}

export interface ErrorIssuesResponse {
  issues: ErrorIssue[];
}

export interface ErrorEvent {
  timestamp: string;
  service: string;
  type: string;
  message: string;
  stacktrace?: string;
  traceId?: string;
  spanId?: string;
  source: string;
  environment?: string;
  sdkName?: string;
  sdkVersion?: string;
  attributes?: Record<string, string>;
}

export interface ErrorEventsResponse {
  events: ErrorEvent[];
  nextCursor?: string;
}

export interface ErrorHistogramPoint {
  time: string;
  count: number;
}

export interface ErrorHistogramResponse {
  points: ErrorHistogramPoint[];
}

// Service health groups (module service-health). Mirrors hub/internal/api/health.go.
export interface HealthDependency {
  service: string;
  tier: string;
  critical: boolean;
  status: HealthStatus;
}

export interface HealthMember {
  service: string;
  tier: string;
  baseStatus: HealthStatus;
  effectiveStatus: HealthStatus;
  reason: string;
  spanCount: number;
  ratePerSec: number;
  errorRate: number;
  p95Ms: number;
  dependencies?: HealthDependency[];
}

export interface HealthGroup {
  name: string;
  tier: string;
  source: "config" | "auto";
  status: HealthStatus;
  reason: string;
  counts: Record<string, number>;
  spanCount: number;
  ratePerSec: number;
  errorRate: number;
  p95Ms: number;
  members: HealthMember[];
}

export interface HealthGroupsResponse {
  overall: HealthStatus;
  checkedAt: string;
  window: { start: string; end: string };
  groups: HealthGroup[];
}

// Alerting (module alerting). Mirrors hub/internal/api/alerts.go (read-only).
export interface FiringAlert {
  rule: string;
  target: string;
  status: HealthStatus;
  since: string;
}

export interface AlertHistoryEntry {
  rule: string;
  target: string;
  kind: "fired" | "resolved";
  status: HealthStatus;
  reason: string;
  firedAt: string;
}

export interface AlertsResponse {
  firing: FiringAlert[];
  history: AlertHistoryEntry[];
}

export interface AlertRule {
  name: string;
  when: string;
  forSec: number;
  channel: string;
  groups?: string[];
  services?: string[];
  tiers?: string[];
}

export interface AlertChannel {
  name: string;
  type: string;
  url: string;
  hasAuth: boolean;
  /** Where the channel is managed: "config" (ConfigMap, read-only) or "ui" (editable). */
  source?: "config" | "ui";
  /** A config channel superseded by a same-name UI channel at delivery time. */
  shadowed?: boolean;
}

export interface AlertRulesResponse {
  rules: AlertRule[];
  channels: AlertChannel[];
}

export interface AlertChannelsResponse {
  channels: AlertChannel[];
}

export interface AlertChannelInput {
  name: string;
  type: "webhook";
  url: string;
  secret?: string;
}

// Auth (Plan A: local only; "oidc" joins in Plan B). /api/v1/auth/config is
// registered even when auth is off — `enabled: false` means no login page
// needed. No 404 special-casing in the SPA.
export interface AuthConfig {
  enabled: boolean;
  methods: ("local" | "oidc")[];
  forceSSO: boolean;
}

export interface AuthGrant {
  scope: string; // project name or "*"
  role: "admin" | "editor" | "viewer";
}

export interface Me {
  user: { id: string; email: string; name: string; anonymous: boolean };
  grants: AuthGrant[];
}
