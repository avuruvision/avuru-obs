// Package storage defines the hub's telemetry-store seam. Handlers depend on
// Store only; all SQL lives in backend packages (see agent_docs/go_style.md).
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// DefaultTenant is the tenant used when none is specified (OSS single-tenant).
const DefaultTenant = "default"

// TimeRange bounds a query. End is exclusive.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// ServiceQuery filters ListServices and ServiceEdges.
type ServiceQuery struct {
	Tenant     string
	Tenants    []string // resolved tenant set; empty means []string{Tenant}
	Range      TimeRange
	ExcludeAux bool // drop health-check/metrics/control-plane traffic
}

// ServiceStats aggregates RED metrics for one service over entry spans.
type ServiceStats struct {
	Name       string
	SpanCount  uint64
	ErrorCount uint64
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
}

// ServiceEdge is a service→service edge on the topology map. It can be derived
// from trace spans (a Client span and the cross-service Server span it spawned,
// giving call volume in Count/ErrorCount) and/or from OBI network flow metrics
// (giving byte volume in Bytes) — the latter surfaces services that emit no
// application traces. Provenance records which source(s) produced the edge:
// "trace", "flow", or "both". Count/ErrorCount stay 0 for flow-only edges;
// Bytes stays 0 for trace-only edges.
type ServiceEdge struct {
	Source     string // caller service (edge tail)
	Target     string // callee service (edge head)
	Count      uint64 // trace call volume
	ErrorCount uint64 // trace errored-call volume
	Bytes      uint64 // network flow bytes (flow-derived edges)
	Provenance string // "trace", "flow", or "both"
	// P50/P95 are CLIENT-side latency for this call path — the caller's Client
	// span duration, so the edge reports what the CALLER experienced (network +
	// queueing + the callee's work). Deliberately not the server span duration,
	// which would just repeat the callee node's own p95 and could not reveal a
	// single slow path into an otherwise healthy service. Zero on flow-derived
	// edges, which have no span to measure.
	P50 time.Duration
	P95 time.Duration
	// ViaTransport names the mesh proxies / gateways a COLLAPSED edge was
	// recovered across (design/2026-08-25-transport-hop-collapse.md). Empty on
	// every directly-observed edge, so an unmeshed map carries no trace of the
	// feature at all.
	ViaTransport []string
	// How much of Count/ErrorCount came through the mesh rather than being
	// observed directly. Equal to Count on a pure recovery; smaller on a pair
	// that talks both ways (a mesh exclusion). A renderer that draws the hops
	// themselves subtracts these, so the same request is never drawn twice.
	CollapsedCount  uint64
	CollapsedErrors uint64
}

// NetworkEdgeHealth is per-edge connection health from OBI's TCP-stats metrics
// (obi.stat.tcp.rtt histogram + the failed-connection and retransmit counters), keyed
// by the same k8s owner endpoints as the flow edges. RTTMs is the p95 over the
// window; FailedConnections is the summed failure count. Reads the
// otel_metrics_* tables, so callers must gate on the infra-metrics module.
type NetworkEdgeHealth struct {
	Source            string
	Target            string
	RTTMs             float64
	FailedConnections uint64
	// Retransmits is packet loss on the link — a signal RTT alone does not
	// carry: a path can retransmit heavily and still look fast. Needs OBI
	// >= v0.12, where obi.stat.tcp.retransmits first exists; on an older
	// sensor it is simply 0 everywhere.
	Retransmits uint64
}

// VirtualTarget is one caller→infrastructure dependency derived from a service's
// EXIT spans: a database, cache or message broker that emits no telemetry of its
// own and is therefore visible only through the spans of the services that call
// it. Rows are grouped per (service, kind, system, peer, direction), so one
// service talking to two databases yields two rows and two services sharing one
// cache yield two rows pointing at the same target.
//
// Direction is "out" for a call the service made (Client/Producer spans) and
// "in" for a message it was delivered (Consumer spans) — without the second
// direction a broker is drawn as a dead end, with producers pointing into it and
// nothing coming out.
//
// Count/ErrorCount/P50/P95 are measured on the CALLER's span, so they report
// what the caller experienced, matching ServiceEdge's client-side rule.
type VirtualTarget struct {
	Service    string // the instrumented service at the near end
	Kind       string // "database" | "cache" | "queue"
	System     string // db.system.name / db.system / messaging.system
	Peer       string // resolved address or logical name; "" when unknown
	Direction  string // "out" (service → target) | "in" (target → service)
	Count      uint64
	ErrorCount uint64
	P50        time.Duration
	P95        time.Duration
}

// CheckResult is one endpoint probe's outcome, as stored.
//
// TraceID is the span the probe emitted for its own request — the join that
// makes a check part of the product rather than a parallel health system: a
// failing check clicks through to the trace explaining why. Empty when no
// gateway endpoint is configured.
type CheckResult struct {
	CheckID   string
	Group     string
	At        time.Time
	OK        bool
	Status    int
	LatencyMs float64
	Error     string
	TraceID   string
	SpanID    string
	// Tenant is set on write and implied by the query on read.
	Tenant string
}

// CheckQuery selects one check's recent results.
type CheckQuery struct {
	Tenant  string
	Tenants []string
	CheckID string
	Limit   int
}

// MeshControlPlane is the health of the service mesh's control plane over the
// window: is it still programming the data plane, and is the data plane
// accepting what it is told?
//
// Available is the load-bearing field. A control plane nobody scrapes reports
// zero rejected configs, which reads as perfect health — so absence is stated
// rather than rendered as zeros, the same way the green module reports "no
// RAPL" instead of 0 W. Every number below is meaningless unless Available.
type MeshControlPlane struct {
	Available bool
	LastSeen  time.Time
	// ConnectedProxies is the latest value in the window, not a sum: it is a
	// gauge, and summing scrapes would multiply it by the scrape count.
	ConnectedProxies uint64
	// Pushes attempted, and configuration the proxies REFUSED. The second is
	// the signal nothing else carries: a rejected push means the control plane
	// and the data plane disagree, and the fleet keeps serving the last config
	// it accepted — indistinguishable from health at every other layer.
	Pushes          uint64
	RejectedConfigs uint64
	// ConvergenceP95Ms is how long a config change takes to reach the proxies.
	ConvergenceP95Ms float64
}

// ZoneTraffic is the byte volume exchanged between two availability zones over
// the window. Zones are node topology, not workload identity: the pair count is
// bounded by how many zones a cluster spans, which is why this can be collected
// without the per-workload flow stream. Same-zone traffic never appears — the
// sensor drops it before export — so every row is a zone crossing.
//
// Reads the otel_metrics_* tables, so callers must gate on the infra-metrics
// module, same as NetworkEdges.
type ZoneTraffic struct {
	SrcZone string
	DstZone string
	Bytes   uint64
}

// TagKey is one business tag key and a bounded sample of the values seen on it.
// Values are for populating a filter control, not for counting: the sample is
// truncated and carries no frequency.
type TagKey struct {
	Key    string
	Values []string
}

// ServiceLabel carries a service's dominant grouping labels, resolved from
// ResourceAttributes over entry spans (argMax by span volume — a service whose
// spans disagree collapses to its single most common value). Used by the
// service-health module to auto-group services by namespace when config does
// not assign them. Either field may be empty (SDK apps that set neither).
type ServiceLabel struct {
	Service          string
	K8sNamespace     string // ResourceAttributes['k8s.namespace.name']
	ServiceNamespace string // ResourceAttributes['service.namespace']
	// Environment is the declared deployment environment: the current semconv
	// key deployment.environment.name, falling back to the deprecated
	// deployment.environment. "" = no environment dimension.
	Environment string
	// DeclaredTier is the raw ResourceAttributes['avuru.tier'] value. It is NOT
	// validated here: application telemetry is untrusted input, so the health
	// package validates it and falls back on garbage rather than failing.
	DeclaredTier string
}

// OverviewQuery filters TraceOverview.
type OverviewQuery struct {
	Tenant     string
	Tenants    []string // resolved tenant set; empty means []string{Tenant}
	Range      TimeRange
	Service    string // optional
	ExcludeAux bool   // drop health-check/metrics/control-plane traffic
}

// OperationStats aggregates RED metrics for one (service, operation) pair
// over entry spans (Server/Consumer).
type OperationStats struct {
	Service    string
	Operation  string
	Count      uint64
	ErrorCount uint64
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
}

// TraceCursor is a keyset-pagination cursor. It carries both the timestamp and
// the root-span duration so it works whichever sort key is active (Duration for
// "slowest", Timestamp otherwise); TraceID is the tiebreaker. Both fields are
// always encoded; only the one matching Order is compared.
type TraceCursor struct {
	Timestamp time.Time
	Duration  time.Duration
	TraceID   string
}

// TraceQuery filters SearchTraces. Zero values mean "no filter".
type TraceQuery struct {
	Tenant      string
	Tenants     []string // resolved tenant set; empty means []string{Tenant}
	Range       TimeRange
	Service     string
	Operation   string
	Status      string            // "", "ok", "error"
	Tags        map[string]string // span-attribute equality filters
	Order       string            // "", "newest" (default), "oldest", "slowest"
	MinDuration time.Duration
	MaxDuration time.Duration
	ExcludeAux  bool // drop health-check/metrics/control-plane traffic
	Limit       int
	Cursor      *TraceCursor
}

// TraceSummary is one root span with per-trace aggregates.
type TraceSummary struct {
	TraceID       string
	RootService   string
	RootOperation string
	StartTime     time.Time
	Duration      time.Duration
	SpanCount     uint64
	ErrorCount    uint64
	StatusCode    string
}

// TracePage is a page of summaries plus the cursor for the next page (nil at
// the end).
type TracePage struct {
	Traces     []TraceSummary
	NextCursor *TraceCursor
}

// SpanEvent is one span event (exception, message, ...).
type SpanEvent struct {
	Time       time.Time
	Name       string
	Attributes map[string]string
}

// Span is one span of a trace, ready for waterfall rendering.
type Span struct {
	TraceID            string
	SpanID             string
	ParentSpanID       string
	Service            string
	Operation          string
	Kind               string
	ScopeName          string // instrumentation library, e.g. "@opentelemetry/instrumentation-http"
	ScopeVersion       string
	StartTime          time.Time
	Duration           time.Duration
	StatusCode         string
	StatusMessage      string
	Attributes         map[string]string
	ResourceAttributes map[string]string
	Events             []SpanEvent
}

// Trace is a full span tree.
type Trace struct {
	TraceID string
	Spans   []Span
}

// HeatmapQuery filters TraceHeatmap.
type HeatmapQuery struct {
	Tenant          string
	Tenants         []string // resolved tenant set; empty means []string{Tenant}
	Range           TimeRange
	Service         string
	Operation       string
	Tags            map[string]string // span-attribute equality filters
	ExcludeAux      bool              // drop health-check/metrics/control-plane traffic
	TimeBuckets     int
	DurationBuckets int
}

// HeatmapCell is one non-empty cell (sparse encoding).
type HeatmapCell struct {
	TimeBucket     int
	DurationBucket int
	Count          uint64
	ErrorCount     uint64
}

// Heatmap is a latency × time histogram over root spans.
type Heatmap struct {
	TimeBucket     time.Duration   // width of one time bucket
	DurationBounds []time.Duration // upper bound per duration bucket (log2)
	Cells          []HeatmapCell
}

// LogCursor is a keyset-pagination cursor for logs: full-precision timestamp
// plus a (TraceId,SpanId) tiebreaker to avoid skips/duplicates.
type LogCursor struct {
	Timestamp time.Time
	TraceID   string
	SpanID    string
}

// LogQuery filters SearchLogs. Zero values mean "no filter".
type LogQuery struct {
	Tenant      string
	Tenants     []string // resolved tenant set; empty means []string{Tenant}
	Range       TimeRange
	Service     string
	MinSeverity string // "", or a severity name (e.g. "ERROR") — matches >= its number
	Query       string // full-text substring on Body (case-insensitive)
	// Tags are equality filters. Keys under the business-tag prefix
	// (avuru.tag.*) match the emitting workload's resource attributes; any
	// other key matches the record's own log attributes.
	Tags   map[string]string
	Limit  int
	Cursor *LogCursor
}

// LogRecord is one log row, ready for the table and trace correlation.
type LogRecord struct {
	Timestamp  time.Time
	Severity   string
	Service    string
	Body       string
	TraceID    string
	SpanID     string
	Attributes map[string]string
}

// LogPage is a page of records plus the cursor for the next page (nil at end).
type LogPage struct {
	Logs       []LogRecord
	NextCursor *LogCursor
}

// REDQuery filters REDSeries. Empty Service means the busiest TopN services.
type REDQuery struct {
	Tenant     string
	Tenants    []string // resolved tenant set; empty means []string{Tenant}
	Range      TimeRange
	Service    string
	Points     int // series buckets (<=0 → backend default)
	TopN       int // services when Service == "" (<=0 → backend default)
	ExcludeAux bool
}

// REDPoint is one time bucket of a service's RED series.
type REDPoint struct {
	Time       time.Time
	Count      uint64
	ErrorCount uint64
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
}

// REDSeries is one service's rate/errors/duration over time (entry spans).
type REDSeries struct {
	Service string
	Points  []REDPoint
}

// InfraQuery filters ListNodeStats / ListPodStats (kubeletstats metrics).
type InfraQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	Node    string // optional: only pods scheduled on this node
	Points  int    // series buckets over Range (<=0 → backend default)
	Limit   int    // pods only: max rows (<=0 → backend default)
}

// MetricPoint is one time-bucketed sample of a series.
type MetricPoint struct {
	Time  time.Time
	Value float64
}

// NodeStat is one node's latest utilization plus short usage series.
type NodeStat struct {
	Name            string
	CPUUsage        float64 // cores
	MemoryUsage     uint64  // bytes
	MemoryAvailable uint64  // bytes
	NetworkRxRate   float64 // bytes/s averaged over Range
	NetworkTxRate   float64 // bytes/s averaged over Range
	PodCount        uint64
	CPUSeries       []MetricPoint
	MemorySeries    []MetricPoint
}

// PodStat is one pod's latest utilization.
type PodStat struct {
	Name        string
	Namespace   string
	Node        string
	Workload    string  // deployment/statefulset/daemonset when known
	CPUUsage    float64 // cores
	MemoryUsage uint64  // bytes
}

// ProfileSample is one ingested profiling sample: a stack (leaf-first
// frames) observed for a service with an aggregate value. The backend
// deduplicates stacks by hash.
type ProfileSample struct {
	Tenant    string
	Timestamp time.Time
	Service   string
	// SampleType is "<type>:<unit>" from the profile (e.g. "samples:count").
	SampleType string
	Frames     []string // leaf-first
	Value      uint64
	Node       string
	Pod        string
	Container  string
}

// ProfileQuery filters ProfileFlamegraph / ListProfiledServices.
type ProfileQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	Service string // required for ProfileFlamegraph
}

// ProfiledService is one service with profiling data in the range.
type ProfiledService struct {
	Name    string
	Samples uint64 // total sample value
}

// FlameNode is one frame in an aggregated flame graph. Value is inclusive
// (self + children); Self is the value sampled exactly at this frame.
// Children are ordered by Value descending.
type FlameNode struct {
	Name     string
	Value    uint64
	Self     uint64
	Children []*FlameNode
}

// AgentQuery filters ListAgentNodes. Window is the lookback within which a
// node counts as "reporting".
type AgentQuery struct {
	Tenant  string
	Tenants []string      // resolved tenant set; empty means []string{Tenant}
	Window  time.Duration // <=0 → backend default
}

// AgentNode is one node with sensor data inside the window. Per-signal
// freshness is nil when that signal has no data from the node — the agent
// inventory is DERIVED from telemetry (no heartbeat protocol until OpAMP,
// v0.2).
type AgentNode struct {
	Node     string
	LastSeen time.Time // newest event across all signals
	Traces   *time.Time
	Logs     *time.Time
	Metrics  *time.Time
	Profiles *time.Time
}

// SignalStats summarizes one telemetry signal's stored data.
type SignalStats struct {
	Signal          string // "traces" | "logs"
	Rows            uint64
	Bytes           uint64 // uncompressed
	CompressedBytes uint64
	Oldest          *time.Time // nil when the signal has no data
	Newest          *time.Time
	// TTLDays is the retention ClickHouse is actually enforcing, read off the
	// tables' TTL expression; 0 when no table for the signal declares one.
	// Reported next to the configured retention because the two can disagree —
	// a chart value changed after the tables were created only takes effect
	// once the migration re-applies the TTL, and until then the number in
	// values.yaml is a wish rather than a fact.
	TTLDays int
}

// DiskStats is one ClickHouse storage disk's capacity.
type DiskStats struct {
	Name       string
	FreeBytes  uint64
	TotalBytes uint64
}

// SystemStats is backend storage health for the System Status view.
type SystemStats struct {
	Signals []SignalStats
	Disks   []DiskStats
}

// TenantSignalUsage is one signal's footprint for ONE project (or, for an
// aggregate, the union of its members). Rows and time bounds are exact —
// they are counted with the tenant filter — but EstimatedBytes is not: parts
// are shared across tenants, so a project's share of a table's compressed
// bytes can only be apportioned by row count. It is labeled an estimate
// everywhere it is shown rather than presented as a measurement.
type TenantSignalUsage struct {
	Signal         string
	Rows           uint64
	EstimatedBytes uint64
	Oldest         *time.Time
	Newest         *time.Time
	// RowsPerMinute is the recent ingest rate, measured over the last hour, so
	// "is this project still sending?" has a number and not just a timestamp.
	RowsPerMinute float64
}

// TenantUsage is one project's share of the store, per signal.
type TenantUsage struct {
	Signals []TenantSignalUsage
}

// SchemaStatus compares the migration ledger against what this install's
// module set expects. Deliberately NOT a Store interface method: it is
// backend-specific bookkeeping rather than a query, and the API receives it as
// an accessor func (like the hot-reloaded config accessors), which keeps every
// existing Store fake untouched.
type SchemaStatus struct {
	Ready    bool
	Database string
	Expected []string
	Applied  []string
	Missing  []string
}

// ErrorIssueQuery filters SearchErrorIssues. Zero values mean "no filter".
type ErrorIssueQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	Status  string // "", "unresolved", "resolved", "ignored" (unresolved includes regressed)
	Service string
	Query   string // case-insensitive substring on type + message
	Sort    string // "", "lastSeen" (default), "count", "firstSeen"
	Limit   int
}

// ErrorIssue is a fingerprint-grouped error, with all-time aggregates so first/
// last seen and regression are correct regardless of the query window.
type ErrorIssue struct {
	Fingerprint uint64
	Service     string
	Type        string
	Message     string
	Source      string
	Status      string // effective status: unresolved | resolved | ignored
	Regressed   bool   // resolved but seen again since — needs attention
	FirstSeen   time.Time
	LastSeen    time.Time
	Count       uint64
	LastTraceID string
}

// ErrorEventCursor is keyset pagination for an issue's occurrence list.
type ErrorEventCursor struct {
	Timestamp time.Time
	TraceID   string
	SpanID    string
}

// ErrorEventQuery lists the occurrences of one issue (by fingerprint).
type ErrorEventQuery struct {
	Tenant      string
	Tenants     []string // resolved tenant set; empty means []string{Tenant}
	Fingerprint uint64
	Range       TimeRange
	Limit       int
	Cursor      *ErrorEventCursor
}

// ErrorEvent is one occurrence of an issue.
type ErrorEvent struct {
	Timestamp   time.Time
	Service     string
	Type        string
	Message     string
	Stacktrace  string
	TraceID     string
	SpanID      string
	Source      string
	Environment string
	SdkName     string
	SdkVersion  string
	Attributes  map[string]string
}

// ErrorEventPage is a page of occurrences plus the next cursor (nil at end).
type ErrorEventPage struct {
	Events     []ErrorEvent
	NextCursor *ErrorEventCursor
}

// ErrorHistogramPoint is one time bucket of an issue's occurrence count.
type ErrorHistogramPoint struct {
	Time  time.Time
	Count uint64
}

// AuthUser is one local or SSO user (auth core). PasswordHash is bcrypt and
// empty for SSO-only users.
type AuthUser struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	Origin       string // "local" | "oidc"
	Disabled     bool
	OidcGroups   []string // raw IdP groups from last SSO login; empty for local
	UpdatedAt    time.Time
}

// AuthGrant is role-on-scope for one user. Scope is a project name or "*".
type AuthGrant struct {
	UserID string
	Scope  string
	Role   string // "admin" | "editor" | "viewer"
}

// AuthSession is one server-side session. TokenHash is hex(sha256(token)):
// the raw token exists only in the cookie.
type AuthSession struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Project is one UI-managed project. ID is immutable (the tenant slug used in
// data and the X-Avuru-Tenant header); Label is display-only; Members is the
// aggregate set (empty for a leaf project — populated in Phase 3).
type Project struct {
	ID      string
	Label   string
	Members []string
	// RetentionDays is how long this project keeps telemetry, in days. 0 means
	// inherit the install's global retention — the common case, and what the
	// column defaults to. A shorter window is enforced by the hub's retention
	// trimmer (TrimTenant), not by a table TTL: the telemetry tables are shared
	// across tenants and a TTL expression cannot be per-value.
	RetentionDays int
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AuthIngestKey is one per-project ingest credential (auth Plan C). KeyHash is
// hex(sha256(raw)) — the raw key is shown to the admin once at creation and
// never stored. Prefix is the raw key's first 12 chars, kept in clear for UI
// identification ("avuruk_ab12…"). Revocation is a tombstone.
type AuthIngestKey struct {
	KeyHash   string
	Project   string
	Name      string
	Prefix    string
	CreatedBy string
	Revoked   bool
	CreatedAt time.Time
}

// AuthToken is one personal API token (design/2026-08-13-api-tokens.md).
// TokenHash is hex(sha256(raw)) — the raw token is shown to its owner once at
// creation and never stored. Prefix is the raw token's first 12 chars, kept
// in clear for UI identification ("avurut_ab12…"). It carries no grants of
// its own: resolution reads the owner's, live, on every request, so a role
// change reaches every token that user holds without hunting them down.
// Revocation is a tombstone.
type AuthToken struct {
	TokenHash  string
	UserID     string
	Name       string
	Prefix     string
	ExpiresAt  time.Time // zero = never expires
	LastUsedAt time.Time // zero = never used
	Revoked    bool
	CreatedAt  time.Time
}

// CostQuery filters the cost module's reads. Reserved capacity is a
// cluster-object fact and usage is a time series, so both halves are read over
// the same window and the same tenant set — a mismatch there would report a
// workload as reserving nothing simply because its objects landed in another
// project.
type CostQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	Limit   int // workloads only: max rows (<=0 → backend default)
}

// WorkloadCost is one workload's reserved capacity against what it used.
//
// Reserved is averaged over the window rather than sampled once: a workload
// that scaled from 2 replicas to 10 reserved more for part of the window, and
// the last sample would report only the end state.
//
// Used carries a peak as well as a mean, because they answer different
// questions and only one of them bounds a right-sizing decision. A request
// cannot be cut below the peak without risking eviction; the mean says how
// much of the time the peak was not happening.
//
// ReservedCPUCores == 0 with a non-zero used is NOT missing data: it is a
// workload that declared no request at all — unschedulable by accident,
// first to be evicted, invisible to every quota. The API reports it as such.
type WorkloadCost struct {
	Workload         string
	Namespace        string
	ReservedCPUCores float64
	ReservedMemBytes float64
	UsedCPUCoresPeak float64
	UsedCPUCoresMean float64
	UsedMemBytesPeak float64
	UsedMemBytesMean float64
	Pods             uint64
}

// NodeCost is one node's allocatable capacity, how much of it is spoken for by
// requests, and how much is actually being used. "89% requested, 12% used" is
// one sentence about two very different problems.
type NodeCost struct {
	Node                string
	AllocatableCPUCores float64
	AllocatableMemBytes float64
	RequestedCPUCores   float64
	RequestedMemBytes   float64
	UsedCPUCores        float64
	UsedMemBytes        float64
}

// GreenQuery filters ServiceEnergy / NodeEnergy (module green). Metric names
// and attribute keys come from the green module's config — the backend must
// not hardcode Kepler naming (an AEP verify item; operators can rename
// without a rebuild).
type GreenQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	// PodEnergyMetrics / NodeEnergyMetrics name the cumulative CPU-energy
	// counters (joules). A query sums deltas across ALL named metrics because
	// Kepler may split energy zones across several metrics.
	PodEnergyMetrics  []string
	NodeEnergyMetrics []string
	// PodNameAttr / PodNamespaceAttr are the metric-attribute keys carrying
	// pod identity on the energy series, joined to the kubeletstats resource
	// attributes for workload attribution.
	PodNameAttr      string
	PodNamespaceAttr string
	// Interval is the bucket width of the Points series (<=0 → backend
	// default derived from Range). Deliberately a duration where sibling
	// queries carry a Points count: the counter-delta math needs
	// deterministic wall-clock bucket boundaries (toStartOfInterval), so a
	// future "harmonize to Points" cleanup must not change this.
	Interval time.Duration
}

// EnergyPoint is one time bucket of an energy series (Wh).
type EnergyPoint struct {
	Time      time.Time
	WattHours float64
}

// ServiceEnergy is one service's energy over a window for ONE quality tier:
// the Wh total plus the bucketed series. An empty Service is the
// unattributed bucket — energy whose pod could not be mapped to a workload
// (the coverage-ratio denominator's missing part, per the green AEP).
// Quality is "measured" (Kepler/RAPL), "estimated" (tdp-estimator), or ""
// (a series with no avuruobs_quality attribute at all — pre-AEP data or a
// misconfigured sensor; callers must not assume "" means measured). A
// service with both measured and estimated energy in the window appears as
// TWO rows, one per quality — callers must never sum across Quality values
// without being explicit about it (the green TDP estimation AEP: never
// silently blend).
type ServiceEnergy struct {
	Service   string
	Quality   string
	WattHours float64
	Points    []EnergyPoint
}

// NodeEnergy is one node's energy over a window for ONE quality tier (Wh
// total + bucketed series) — same Quality semantics as ServiceEnergy.
type NodeEnergy struct {
	Node      string
	Quality   string
	WattHours float64
	Points    []EnergyPoint
}

// NodeCoverage reports, per node, whether it contributed measured,
// estimated, or no green energy in the window — closing the green-carbon
// AEP review's follow-up (the RAPL-less share was invisible before this).
// "Known nodes" is the node universe visible in recent telemetry (the same
// k8s.node.name resource attribute the whole infra view keys on), not a
// heartbeat protocol. AbsentNodes = KnownNodes - MeasuredNodes -
// EstimatedNodes (a node reporting BOTH tiers, which shouldn't normally
// happen per-node but isn't impossible on a heterogeneous multi-NIC node,
// counts toward both — AbsentNodes is therefore a lower bound in that edge
// case, never negative).
type NodeCoverage struct {
	KnownNodes     int
	MeasuredNodes  int
	EstimatedNodes int
	AbsentNodes    int
	// Nodes names the known-node universe (sorted), so callers can render
	// per-node detail — including absent nodes at zero — without a second
	// known-nodes query drifting from the counts above.
	Nodes []string
}

// Store is the telemetry query seam implemented by storage backends.
type Store interface {
	Ping(ctx context.Context) error
	SystemStats(ctx context.Context) (SystemStats, error)
	ListServices(ctx context.Context, q ServiceQuery) ([]ServiceStats, error)
	// ServiceLabels returns each service's dominant grouping labels (namespace)
	// over the same entry-span population as ListServices. Used by the
	// service-health module to auto-group unassigned services by namespace.
	ServiceLabels(ctx context.Context, q ServiceQuery) ([]ServiceLabel, error)
	ServiceEdges(ctx context.Context, q ServiceQuery) ([]ServiceEdge, error)
	// CollapsedEdges recovers the app→app dependencies a service mesh hides,
	// by walking each trace's parent chain across the transport spans named in
	// `transport` (the classified proxy/gateway set, resolved by the caller).
	// An empty set means no mesh and the call is free — implementations return
	// without querying. Core: it reads otel_traces, like ServiceEdges.
	CollapsedEdges(ctx context.Context, q ServiceQuery, transport []string) ([]ServiceEdge, error)
	// TagKeys returns the business tags (avuru.tag.*) present on telemetry in
	// the window with a bounded sample of each one's values, for filter
	// discovery.
	TagKeys(ctx context.Context, q ServiceQuery) ([]TagKey, error)
	// NetworkEdges derives service→service edges from OBI network flow metrics
	// (otel_metrics_sum). It reads the metrics tables, so callers must gate it
	// on the infra-metrics module being active (the tables exist only then).
	// VirtualTargets derives the infrastructure dependencies (databases, caches,
	// message brokers) that appear only in their callers' exit spans. Core: it
	// reads otel_traces, so it needs no module gate.
	VirtualTargets(ctx context.Context, q ServiceQuery) ([]VirtualTarget, error)
	NetworkEdges(ctx context.Context, q ServiceQuery) ([]ServiceEdge, error)
	// NetworkEdgeHealth returns per-edge RTT p95 + failed-connection counts from
	// OBI's TCP-stats metrics. Same infra-metrics gating as NetworkEdges.
	NetworkEdgeHealth(ctx context.Context, q ServiceQuery) ([]NetworkEdgeHealth, error)
	// ZoneTraffic returns bytes exchanged per (src zone, dst zone) pair over the
	// window, from the sensor's inter-zone counters. Same infra-metrics gating
	// as NetworkEdges, and the same cumulative-counter caveat.
	ZoneTraffic(ctx context.Context, q ServiceQuery) ([]ZoneTraffic, error)
	// MeshControlPlane summarises the service mesh's control plane over the
	// window. Reads the metrics tables (the scrape lands there), so the same
	// infra-metrics gating as NetworkEdges applies on top of the mesh module.
	MeshControlPlane(ctx context.Context, q ServiceQuery) (MeshControlPlane, error)
	// RecordCheckResult appends one endpoint probe's outcome. Append-only: a
	// check that flapped is a fact worth keeping, not state to overwrite.
	//
	// Takes the storage shape rather than the scheduler's: `checks` imports
	// `health`, `health` imports `storage`, so a storage method typed on
	// checks.Result would close an import cycle. cmd/hub adapts between them,
	// which is where the two halves already meet.
	RecordCheckResult(ctx context.Context, r CheckResult) error
	// CheckResults returns one endpoint check's recent outcomes, newest first.
	// Owned by the service-health module (migration 0020).
	CheckResults(ctx context.Context, q CheckQuery) ([]CheckResult, error)
	// LatestCheckStates returns up to perCheck recent results per check in the
	// window — what the consecutive-failure rule needs, and no more.
	LatestCheckStates(ctx context.Context, q ServiceQuery, perCheck int) (map[string][]CheckResult, error)
	TraceOverview(ctx context.Context, q OverviewQuery) ([]OperationStats, error)
	SearchTraces(ctx context.Context, q TraceQuery) (TracePage, error)
	// GetTrace/FindSpanTrace/LogsForTrace (and the error-issue reads below)
	// take the resolved tenant set — same semantics as the Tenants query
	// field, but with no single-tenant fallback: callers pass at least one.
	GetTrace(ctx context.Context, tenants []string, traceID string) (Trace, error)
	// FindSpanTrace resolves the trace containing spanID, or ErrNotFound.
	FindSpanTrace(ctx context.Context, tenants []string, spanID string) (traceID string, err error)
	TraceHeatmap(ctx context.Context, q HeatmapQuery) (Heatmap, error)
	SearchLogs(ctx context.Context, q LogQuery) (LogPage, error)
	LogsForTrace(ctx context.Context, tenants []string, traceID string) ([]LogRecord, error)
	ListNodeStats(ctx context.Context, q InfraQuery) ([]NodeStat, error)
	ListPodStats(ctx context.Context, q InfraQuery) ([]PodStat, error)
	ListAgentNodes(ctx context.Context, q AgentQuery) ([]AgentNode, error)
	// ListTenants returns tenants observed in recent data (projects
	// auto-discovery; config-defined projects merge in at the API layer).
	ListTenants(ctx context.Context) ([]string, error)
	REDSeries(ctx context.Context, q REDQuery) ([]REDSeries, error)
	WriteProfileSamples(ctx context.Context, samples []ProfileSample) error
	ListProfiledServices(ctx context.Context, q ProfileQuery) ([]ProfiledService, error)
	ProfileFlamegraph(ctx context.Context, q ProfileQuery) (FlameNode, error)
	// Error tracking (module error-tracking).
	SearchErrorIssues(ctx context.Context, q ErrorIssueQuery) ([]ErrorIssue, error)
	GetErrorIssue(ctx context.Context, tenants []string, fingerprint uint64) (ErrorIssue, error)
	ListErrorEvents(ctx context.Context, q ErrorEventQuery) (ErrorEventPage, error)
	ErrorIssueHistogram(ctx context.Context, tenants []string, fingerprint uint64, r TimeRange, points int) ([]ErrorHistogramPoint, error)
	// SetErrorIssueStatus records a triage decision (unresolved|resolved|ignored).
	SetErrorIssueStatus(ctx context.Context, tenant string, fingerprint uint64, status string) error
	// Green energy (module green; requires infra-metrics — the pod→workload
	// attribution reads kubeletstats resource attributes). ServiceEnergy
	// returns per-service Wh totals + bucketed series over the window,
	// heaviest first; a row with empty Service is the unattributed bucket.
	// NodeEnergy is the per-node equivalent from the node counters; the
	// summary endpoint joins it with NodeCoverage's node universe for the
	// per-node table.
	// Storage returns energy only (Wh) — carbon factors never enter SQL;
	// gCO2e is computed by callers.
	ServiceEnergy(ctx context.Context, q GreenQuery) ([]ServiceEnergy, error)
	NodeEnergy(ctx context.Context, q GreenQuery) ([]NodeEnergy, error)
	NodeCoverage(ctx context.Context, q GreenQuery) (NodeCoverage, error)
	// Cost (module cost).
	WorkloadCosts(ctx context.Context, q CostQuery) ([]WorkloadCost, error)
	NodeCosts(ctx context.Context, q CostQuery) ([]NodeCost, error)
	// Alerting (module alerting).
	LoadAlertStates(ctx context.Context, tenant string) ([]AlertState, error)
	SaveAlertStates(ctx context.Context, states []AlertState) error
	AppendAlertHistory(ctx context.Context, entries []AlertHistoryEntry) error
	ListAlertHistory(ctx context.Context, q AlertHistoryQuery) ([]AlertHistoryEntry, error)
	// UI-managed alert channels (global, not per-tenant — the notification
	// payload carries the tenant). SaveAlertChannel upserts by Name;
	// DeleteAlertChannel returns ErrNotFound when no live channel has the name.
	ListAlertChannels(ctx context.Context) ([]AlertChannel, error)
	SaveAlertChannel(ctx context.Context, ch AlertChannel) error
	DeleteAlertChannel(ctx context.Context, name string) error
	// Collection overlay (runtime sensor toggle — design/
	// 2026-07-27-collection-control-plane.md). LoadCollectionOverlay returns
	// ErrNotFound when no overlay has ever been saved. SaveCollectionOverlay
	// upserts the singleton; saving an empty ("{}"-equivalent) Overlay is how
	// the API layer implements "reset to chart defaults" — there is no
	// separate delete method.
	LoadCollectionOverlay(ctx context.Context) (CollectionOverlay, error)
	SaveCollectionOverlay(ctx context.Context, ov CollectionOverlay) error
	// Auth (core): local users, per-project grants, server-side sessions.
	// GetAuthUserByEmail/GetAuthUser return ErrNotFound for unknown users;
	// disabled users ARE returned (callers decide). SaveAuthUser upserts by
	// ID. DeleteAuthUser tombstones a user (ErrNotFound if no live user has
	// the id); every read path then reports ErrNotFound too. It does NOT
	// touch the user's sessions or grants — callers that want those revoked
	// too must call RevokeAuthSessionsForUser/ReplaceAuthGrants(nil)
	// themselves. A later SaveAuthUser for the same ID resurrects a fresh
	// live row (SSO re-provisioning). ReplaceAuthGrants replaces the user's
	// grant set (tombstone missing scopes, upsert the rest). GetAuthSession
	// returns ErrNotFound for unknown, revoked or expired sessions.
	// RevokeAuthSession likewise returns ErrNotFound for a token that is
	// unknown, already revoked, or already expired.
	GetAuthUser(ctx context.Context, id string) (AuthUser, error)
	GetAuthUserByEmail(ctx context.Context, email string) (AuthUser, error)
	ListAuthUsers(ctx context.Context) ([]AuthUser, error)
	SaveAuthUser(ctx context.Context, u AuthUser) error
	DeleteAuthUser(ctx context.Context, id string) error
	ListAuthGrants(ctx context.Context, userID string) ([]AuthGrant, error)
	ReplaceAuthGrants(ctx context.Context, userID string, grants []AuthGrant) error
	CreateAuthSession(ctx context.Context, s AuthSession) error
	GetAuthSession(ctx context.Context, tokenHash string) (AuthSession, error)
	RevokeAuthSession(ctx context.Context, tokenHash string) error
	// RevokeAuthSessionsForUser revokes every live session belonging to
	// userID in one operation (keyed by user, not by token) — used on
	// password rotation, so a compromised account's existing cookies stop
	// working the moment the password changes. A no-op (nil error) when the
	// user has no live sessions.
	RevokeAuthSessionsForUser(ctx context.Context, userID string) error
	// UI-managed projects (Phase 1). Reads are live-only (tombstones filtered).
	// GetProject returns ErrNotFound for an absent or deleted id; SaveProject
	// upserts by ID; DeleteProject tombstones and returns ErrNotFound when no
	// live project has the id.
	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, id string) (Project, error)
	SaveProject(ctx context.Context, p Project) error
	DeleteProject(ctx context.Context, id string) error
	// TrimTenant enforces a project's own retention window: it deletes that
	// tenant's telemetry older than cutoff and returns the tables it acted on.
	// It is the mechanism behind Project.RetentionDays — a per-tenant window
	// cannot be a table TTL — and is called only by the hub's retention
	// trimmer, never from a request path: a mutation is a part rewrite, not a
	// cheap delete. Doing nothing (no such table, a trim still running, no row
	// old enough) is success with an empty list, not an error.
	TrimTenant(ctx context.Context, tenant string, cutoff time.Time) ([]string, error)
	// TenantUsage reports what ONE project holds — rows, an apportioned byte
	// estimate, freshness and a recent ingest rate per signal — for the
	// per-project half of Settings → Status. tenants is the resolved set, so an
	// aggregate reports its members' union exactly as its screens read it.
	// Absent tables (module off) are skipped, never an error.
	TenantUsage(ctx context.Context, tenants []string, now time.Time) (TenantUsage, error)
	// UI-authored service health groups (module service-health). Chart-declared
	// groups stay in the ConfigMap and are NOT stored here — health.Resolver
	// merges the two and lets the config win a name collision (design/
	// 2026-08-07-service-groups-crud.md). Name is the identity, so
	// SaveServiceGroup upserts by it; DeleteServiceGroup tombstones and
	// returns ErrNotFound when no live group has the name.
	ListServiceGroups(ctx context.Context) ([]ServiceGroup, error)
	SaveServiceGroup(ctx context.Context, g ServiceGroup) error
	DeleteServiceGroup(ctx context.Context, name string) error
	// UI-authored OIDC group->role mapping rules. Chart-declared rules stay in
	// the OIDC ConfigMap and are NOT stored here — auth.MergeMapping merges the
	// two and lets the config win a name collision, same shape as
	// ListServiceGroups. Group is the identity, so SaveOIDCGroupMapping
	// upserts by it; DeleteOIDCGroupMapping tombstones and returns
	// ErrNotFound when no live rule has the group.
	ListOIDCGroupMappings(ctx context.Context) ([]OIDCGroupMapping, error)
	SaveOIDCGroupMapping(ctx context.Context, m OIDCGroupMapping) error
	DeleteOIDCGroupMapping(ctx context.Context, group string) error
	// ResetOIDCGroupMappings tombstones every UI-authored rule, returning the
	// install to exactly what the chart declares.
	ResetOIDCGroupMappings(ctx context.Context) error
	// Ingest keys (auth Plan C). GetIngestKeyByHash returns ErrNotFound for an
	// unknown OR revoked key (the gateway caches this as a negative verdict).
	// CreateIngestKey inserts a live key. ListIngestKeys returns the live keys
	// for one project, newest first. RevokeIngestKey tombstones by hash and
	// returns ErrNotFound when no live key with that hash exists in the project.
	CreateIngestKey(ctx context.Context, k AuthIngestKey) error
	GetIngestKeyByHash(ctx context.Context, keyHash string) (AuthIngestKey, error)
	ListIngestKeys(ctx context.Context, project string) ([]AuthIngestKey, error)
	RevokeIngestKey(ctx context.Context, project, keyHash string) error
	// Personal API tokens (design/2026-08-13-api-tokens.md). Same tombstone
	// shape as ingest keys, scoped by owner instead of project.
	// GetAuthTokenByHash returns ErrNotFound for an unknown OR revoked token,
	// but returns an EXPIRED token normally — expiry is decided one layer up
	// (in the auth package, not storage), so the owner can still see why it
	// stopped working. ListAuthTokens returns the live tokens for one user,
	// newest first, including expired ones, for the same reason.
	// RevokeAuthToken is scoped by userID as well as hash, so a revoke URL
	// cannot tombstone another user's token by guessing the hash.
	// TouchAuthToken re-inserts the row with a new LastUsedAt (a
	// ReplacingMergeTree upsert, not an ALTER) — callers are responsible for
	// debouncing this; storage does not.
	CreateAuthToken(ctx context.Context, t AuthToken) error
	GetAuthTokenByHash(ctx context.Context, tokenHash string) (AuthToken, error)
	ListAuthTokens(ctx context.Context, userID string) ([]AuthToken, error)
	RevokeAuthToken(ctx context.Context, userID, tokenHash string) error
	TouchAuthToken(ctx context.Context, tokenHash string, at time.Time) error
}

// CollectionOverlay is the persisted runtime collection overlay (design/
// 2026-07-27-collection-control-plane.md). Overlay is an opaque JSON blob —
// its schema is owned and validated by package collection, not here.
type CollectionOverlay struct {
	Overlay   string
	UpdatedAt time.Time
	UpdatedBy string
}

// ServiceGroup is a UI-authored service health group. It carries the wire
// shape of health.Group flattened (Tier as a plain string, the selector as two
// slices) so storage stays free of the health package's vocabulary — the
// conversion lives in health.Resolver, which owns the merge.
type ServiceGroup struct {
	Name       string
	Tier       string
	Namespaces []string
	Services   []string
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OIDCGroupMapping is one UI-authored IdP-group -> role-on-projects rule. It
// overlays the chart-declared mapping in the OIDC ConfigMap, which stays the
// base and stays hot-reloading; the hub never writes that ConfigMap.
type OIDCGroupMapping struct {
	Group     string
	Role      string
	Projects  []string
	CreatedBy string
	UpdatedAt time.Time
}

// AlertChannel is a UI-managed delivery channel (global, not per-tenant).
type AlertChannel struct {
	Name      string
	Type      string // "webhook" (v1)
	URL       string
	Secret    string
	UpdatedAt time.Time
}

// AlertState is the evaluator's durable memory for one rule×target: whether it
// is ok, pending (condition true, waiting out the `for` timer) or firing.
type AlertState struct {
	Tenant         string
	RuleName       string
	Target         string
	Status         string // ok | pending | firing
	Since          time.Time
	LastNotifiedAt time.Time
}

// AlertHistoryEntry is one fire/resolve event for the Alerts UI timeline.
type AlertHistoryEntry struct {
	Tenant   string
	RuleName string
	Target   string
	Kind     string // fired | resolved
	Status   string // the health status at the time (down|degraded|healthy)
	Reason   string
	FiredAt  time.Time
}

// AlertHistoryQuery filters ListAlertHistory.
type AlertHistoryQuery struct {
	Tenant  string
	Tenants []string // resolved tenant set; empty means []string{Tenant}
	Range   TimeRange
	Limit   int
}
