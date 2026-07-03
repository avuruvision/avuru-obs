// Hand-written M1 mirror of the hub API DTOs (hub/internal/api/dto.go).
// Replaced by proto/buf codegen in a follow-up — keep field names in sync.

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
