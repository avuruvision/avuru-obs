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
  | "alerting"
  | "green";

export interface CapabilitiesResponse {
  version: string;
  modules: ModuleName[];
  // Whether /api/v1/collection/overlay is live (chart flag
  // collection.runtimeControl.enabled). Absent on hubs predating the flag —
  // treat as false.
  collectionRuntimeControl?: boolean;
}

export interface ServiceStats {
  name: string;
  spanCount: number;
  ratePerSec: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
  // Energy overlay (module green): the service map stamps these only when the
  // green module is active; omitted (omitempty) otherwise — a non-green install
  // returns a byte-identical service shape.
  wh?: number;
  gco2e?: number;
  // "transport" when the hub classified this workload as infrastructure that
  // carries other services' traffic; "virtual" when the node is a dependency
  // that sends no telemetry of its own and was derived from its callers' exit
  // spans. Absent for applications, so a cluster with neither returns the shape
  // it always did.
  role?: string; // "transport" | "virtual"
  // NOTE: the map renderer also synthesizes role "peer" client-side for an edge
  // endpoint it could not resolve to a service (lib/map-peers.ts). The hub never
  // sends it — it is a statement about what the RENDERER could not resolve.
  // What a virtual target actually is. Only ever set beside role "virtual".
  kind?: string; // "database" | "cache" | "queue"
  // Where the workload lives — k8s.namespace.name, or service.namespace for a
  // pure-SDK app that declares one. Stamped by the service map only, and absent
  // when the service declares neither, which the map draws as "no boundary".
  namespace?: string;
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
  // Client-side latency for this call path — what the CALLER experienced
  // (network + queueing + callee work). Absent on flow-derived edges, which
  // have no span to measure: treat absent as "not measured", never as 0.
  p50Ms?: number;
  p95Ms?: number;
  // The mesh proxies / gateways this dependency was recovered across. Present
  // only on edges the hub reconstructed by walking a trace's parent chain over
  // the transport hops — see design/2026-08-25-transport-hop-collapse.md.
  viaTransport?: string[];
  // How much of `calls` / `errorCount` crossed a proxy. Subtract to get the
  // directly-observed remainder when the hops are being drawn themselves.
  collapsedCalls?: number;
  collapsedErrorCount?: number;
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
  /** What this install is configured to keep. */
  retentionDays: number;
  /** What ClickHouse is actually enforcing (0 = no day-based TTL found). */
  ttlDays: number;
}

/** Where telemetry is stored. Read-only by nature: ClickHouse is the store, so
 *  it cannot hold its own connection string. Never carries a credential. */
export interface StorageConnection {
  address: string;
  database: string;
  username?: string;
  protocol: string;
}

export interface DiskStats {
  name: string;
  freeBytes: number;
  totalBytes: number;
}

/** One signal's footprint for the SELECTED project. Rows, the bounds and the
 *  rate are counted with the tenant filter; estimatedBytes is apportioned from
 *  the table's compressed size by row share (parts are shared across tenants),
 *  which is why it is shown as an estimate. */
export interface ProjectSignalUsage {
  signal: string;
  rows: number;
  estimatedBytes: number;
  oldest?: string;
  newest?: string;
  rowsPerMinute: number;
  /** What THIS project keeps for the signal; `inherited` says whether that is
   *  its own window or the install-wide one. */
  retentionDays: number;
  inherited: boolean;
}

/** What the selected project holds, as opposed to what the install holds. An
 *  aggregate reports the union of the members the viewer may see and names
 *  them in `tenants`. */
export interface ProjectUsage {
  id: string;
  tenants: string[];
  /** An aggregate whose members keep different windows has no single honest
   *  number, so the UI points at the members instead. */
  retentionVaries?: boolean;
  signals: ProjectSignalUsage[];
}

export interface SystemStatusResponse {
  version: string;
  overall: "healthy" | "degraded" | "down";
  checkedAt: string;
  components: ComponentHealth[];
  signals: SignalStats[];
  disks: DiskStats[];
  connection?: StorageConnection;
  /** Absent when the per-project read failed — the instance-wide half of the
   *  page still renders. */
  project?: ProjectUsage;
}

// Permissions matrix (GET /api/v1/auth/permissions). Derived by the hub from
// the guards its routes registered with, so the SPA never holds a second copy
// of the authorization rules.
export interface PermissionRole {
  role: string;
  label: string;
  description: string;
}

export interface PermissionArea {
  area: string;
  label: string;
  /** Lowest role that can read it; absent when there is no such route. */
  read?: string;
  /** Lowest role that can change it; absent when the area is read-only. */
  write?: string;
}

export interface PermissionsResponse {
  roles: PermissionRole[];
  areas: PermissionArea[];
  authEnabled: boolean;
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

// Bytes that crossed an availability-zone boundary, per zone pair. Direction
// matters — a -> b and b -> a are separate rows, as they are on a cloud bill.
export interface ZoneTraffic {
  srcZone: string;
  dstZone: string;
  bytes: number;
}

export interface ZonesResponse {
  zones: ZoneTraffic[];
}

// A business tag mapped from a Kubernetes label at collection. `key` is the
// full attribute a filter string carries (avuru.tag.team); `name` is what a
// person calls it (team). Values are a bounded sample for a filter control, not
// a complete or ranked list.
export interface TagKey {
  key: string;
  name: string;
  values: string[];
}

export interface TagsResponse {
  tags: TagKey[];
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
  label?: string;
  source: "default" | "config" | "db" | "data" | "granted";
  editable?: boolean;
  members?: string[];
  // Absent or 0: this project inherits the install-wide retention. A positive
  // value is its own (shorter) window, enforced by the hub's retention trimmer.
  retentionDays?: number;
}

export interface ProjectsResponse {
  projects: Project[];
}

// Request bodies for the admin project-CRUD endpoints (mirror the hub Go DTOs
// createProjectRequest / updateProjectRequest in hub/internal/api/projects.go).
export interface CreateProjectRequest {
  id: string;
  label: string;
  retentionDays?: number;
}

// PUT /projects/{id} is a partial update: an omitted field keeps its stored
// value, so the label editor and the members editor can each send only theirs.
export interface UpdateProjectRequest {
  label?: string;
  members?: string[];
  retentionDays?: number;
}

// Per-project ingest keys (auth Plan C). List/metadata only ever carries the
// prefix + hash; the raw secret appears exactly once in CreateIngestKeyResponse.
export interface IngestKey {
  keyHash: string;
  prefix: string;
  name: string;
  createdBy: string;
  createdAt: string;
}

export interface IngestKeysResponse {
  keys: IngestKey[];
}

export interface CreateIngestKeyRequest {
  name: string;
}

// The ONE response carrying the raw `key`. It is never returned again — the UI
// shows it once in a copy dialog and warns it cannot be recovered.
export interface CreateIngestKeyResponse {
  key: string;
  keyHash: string;
  prefix: string;
  name: string;
  project: string;
  createdAt: string;
}

// Personal API tokens (Settings → Access) — non-interactive access that acts
// as its OWNER, grants read live at request time. Mirrors apiTokenDTO in
// hub/internal/api/tokens.go. tokenHash is the revoke handle (a
// preimage-resistant hash, not a secret; the prefix is not unique).
// lastUsedAt/expiresAt are absent for "never used" / "never expires" rather
// than zero-epoch dates, so neither can be mistaken for a real one.
export interface ApiToken {
  tokenHash: string;
  prefix: string;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
}

export interface ApiTokensResponse {
  tokens: ApiToken[];
}

// expiresInDays zero or absent means the token never expires.
export interface CreateApiTokenRequest {
  name: string;
  expiresInDays?: number;
}

// The ONE response carrying the raw `token`. It is never returned again — the
// UI shows it once in a copy dialog and warns it cannot be recovered.
export interface CreateApiTokenResponse {
  token: string;
  tokenHash: string;
  prefix: string;
  name: string;
  createdAt: string;
  expiresAt?: string;
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
  // Declared deployment environment. Absent = the service declared none, and
  // the group is identified by name alone (pre-environment behavior).
  environment?: string;
  tier: string;
  source: "config" | "auto";
  tierSource: "override" | "config" | "declared" | "default";
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
  // Declarations the hub could not honour (e.g. an invalid avuru.tier).
  warnings?: string[];
}

// Group DEFINITIONS (what the groups are), as opposed to HealthGroup above
// (how they are doing). Mirrors hub/internal/api/service_groups.go.
export interface ServiceGroupDef {
  name: string;
  tier: string;
  namespaces: string[];
  services: string[];
  /** Where the group is managed: "config" (chart values, read-only) or "db" (authored here). */
  source: "config" | "db";
  editable: boolean;
  /** An authored group whose name the chart config has since taken: still stored, no longer grouping. */
  shadowed?: boolean;
}

export interface ServiceGroupsResponse {
  groups: ServiceGroupDef[];
}

export interface ServiceGroupInput {
  name: string;
  tier: string;
  namespaces: string[];
  services: string[];
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
  demoEnabled?: boolean;
}

export interface AuthGrant {
  scope: string; // project name or "*"
  role: "admin" | "editor" | "viewer";
}

export interface Me {
  // origin: "local" | "oidc" | "" (empty for the anonymous identity) — how the
  // user signed in, for display. It is NOT the test for whether a password
  // form applies; passwordChange is (the demo viewer is a local account that
  // still cannot rotate its credential).
  //
  // passwordChange: "self" | "idp" | "shared" | "" (anonymous) — whether
  // self-service rotation applies, and if not, why. Mirrors
  // hub/internal/api/auth_handlers.go's passwordChangeFor.
  //
  // Both are plain strings, not narrowed unions — "" is a real value and a
  // future origin or refusal reason must not break the build. Always present
  // (no omitempty on the Go side).
  user: {
    id: string;
    email: string;
    name: string;
    origin: string;
    anonymous: boolean;
    passwordChange: string;
  };
  grants: AuthGrant[];
}

// User administration (Plan A; admin only). Mirrors hub/internal/api/users.go.
export interface AdminUser {
  id: string;
  email: string;
  name: string;
  origin: string;
  disabled: boolean;
  grants: AuthGrant[];
}

export interface UsersResponse {
  users: AdminUser[];
}

export interface CreateUserRequest {
  email: string;
  name: string;
  password: string;
  grants: AuthGrant[];
}

// PUT /api/v1/users/{id} — absent field = leave unchanged; grants replace
// the whole set. Mirrors updateUserRequest in hub/internal/api/users.go.
export interface UpdateUserRequest {
  name?: string;
  password?: string;
  disabled?: boolean;
  grants?: AuthGrant[];
}

// POST /api/v1/auth/password (self-service; answers the refreshed Me).
export interface ChangePasswordRequest {
  currentPassword: string;
  newPassword: string;
}

// OIDC group→role mapping overlay (Settings → Access, SSO installs only).
// Mirrors oidcMappingDTO in hub/internal/api/oidc_mapping.go. Source "config"
// is the chart's auth.oidc.mapping values — read-only, hot-reloaded; "db" is
// authored from this API. Shadowed: an authored rule whose group the config
// ALSO declares — still stored and still editable, but the config wins, so
// it grants nothing (auth.MergeMapping). Invalid: a stored row whose role no
// longer parses — role is absent (it grants nothing), and only the raw
// invalidRole it was saved with is carried, so it can be shown and deleted.
export interface OIDCMappingRule {
  group: string;
  role?: string;
  projects?: string[];
  source: "config" | "db";
  editable: boolean;
  shadowed?: boolean;
  invalid?: boolean;
  invalidRole?: string;
}

export interface OIDCMappingResponse {
  rules: OIDCMappingRule[];
}

// PUT /api/v1/auth/oidc/mapping/{group} body — mirrors oidcMappingInput in
// oidc_mapping.go exactly. The group is the path segment, not a body field.
export interface OIDCMappingInput {
  role: string;
  projects: string[];
}

// Green — per-service energy & carbon (module green). Mirrors the Go DTOs in
// hub/internal/api/green.go and green_budgets.go — field names are byte-exact
// with the json tags there (e.g. gridIntensity/intensitySource, not
// intensity/source; gco2e, not totalGCO2e; burnDown, not burndown).

// Carbon conversion factors in force for this install.
export interface GreenFactors {
  gridIntensity: number; // gCO2e/kWh
  intensitySource: string; // provenance ("operator-set" or bundled default label)
  pue: number;
  dataset: string; // the bundled factor table the defaults come from
}

export interface GreenTotals {
  attributedWh: number;
  // measuredWh/estimatedWh split attributedWh by quality tier (RAPL/Kepler
  // vs the tdp-estimator) — never blended into one number (green TDP
  // estimation AEP).
  measuredWh: number;
  estimatedWh: number;
  unattributedWh: number;
  coverage: number; // attributed / (attributed + unattributed), 0..1
  gco2e: number; // carbon for attributed + unattributed energy
  nodeCoverage: GreenNodeCoverage;
}

// Per-node green coverage: how many known nodes reported measured energy,
// estimated energy, or neither (absent) in the window.
export interface GreenNodeCoverage {
  known: number;
  measured: number;
  estimated: number;
  absent: number;
}

export interface GreenEnergyPoint {
  time: string;
  wh: number;
}

// One row of the per-service energy table. requests/mgCO2ePerRequest/points/
// estimatedWh are omitempty in Go: absent for the synthetic (other)/
// (unattributed) rows, a service that saw no requests, or energy that is
// entirely measured.
export interface GreenServiceEnergy {
  service: string;
  wh: number;
  estimatedWh?: number;
  gco2e: number;
  requests?: number;
  mgCO2ePerRequest?: number;
  points?: GreenEnergyPoint[];
}

// One row of the per-node energy table: every KNOWN node, absent ones at 0 Wh.
// quality is "measured" | "estimated" | "absent", or "" for energy carrying no
// avuruobs_quality attribute (pre-AEP data — never assumed measured).
export interface GreenNodeEnergy {
  node: string;
  wh: number;
  estimatedWh?: number;
  quality: string;
}

export interface GreenSummaryResponse {
  window: { start: string; end: string };
  factors: GreenFactors;
  totals: GreenTotals;
  services: GreenServiceEnergy[];
  nodes?: GreenNodeEnergy[];
}

export interface GreenBurnPoint {
  time: string;
  kgCO2e: number; // cumulative, in the budget's own unit
}

// A carbon budget's month-to-date status. status is "ok" | "warn" | "exceeded".
export interface GreenBudget {
  name: string;
  group: string;
  monthlyKgCO2e: number;
  usedKgCO2e: number;
  projectedKgCO2e: number; // linear month-end projection
  ratio: number;
  status: "ok" | "warn" | "exceeded";
  burnDown?: GreenBurnPoint[];
  // Fraction of usedKgCO2e that came from modeled (tdp-estimator) rather
  // than measured energy — omitted when 0.
  estimatedShare?: number;
  // Whether a threshold crossing on THIS budget can actually be delivered.
  // Each value names a different fix: enable the alerting module, give the
  // budget a channel, or correct the channel name it points at.
  notifications: "ok" | "alerting-off" | "no-channel" | "unknown-channel";
}

export interface GreenBudgetsResponse {
  window: { start: string; end: string };
  budgets: GreenBudget[];
  // Configuration problems that leave a budget inert without making it look
  // broken (today: a group nothing rolls up to).
  warnings?: string[];
}

// Runtime collection overlay (design/2026-07-27-collection-control-plane.md).
// Mirrors hub/internal/collection.Overlay: an absent field means "not
// overridden — the chart's base value applies", never "off".
export interface CollectionOverlay {
  obiEnabled?: boolean;
  logsEnabled?: boolean;
  kubeletstatsEnabled?: boolean;
  profilerEnabled?: boolean;
  greenEnabled?: boolean;
  excludeNamespaces?: string[];
}

// The resolved base ⊕ overlay state — what the sensor actually collects.
export interface CollectionEffective {
  obi: boolean;
  logs: boolean;
  kubeletstats: boolean;
  profiler: boolean;
  green: boolean;
  excludeNamespaces: string[];
}

export interface CollectionOverlayResponse {
  overlay: CollectionOverlay;
  // Absent when the hub has no in-cluster applier to resolve it (or the
  // cluster read failed). Absent means "unknown here" — the UI must not
  // render that as "nothing is being collected".
  effective?: CollectionEffective;
  updatedAt?: string;
  updatedBy?: string;
}
