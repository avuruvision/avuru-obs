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
}

// NetworkEdgeHealth is per-edge connection health from OBI's TCP-stats metrics
// (obi.stat.tcp.rtt histogram + obi.stat.tcp.failed.connections counter), keyed
// by the same k8s owner endpoints as the flow edges. RTTMs is the p95 over the
// window; FailedConnections is the summed failure count. Reads the
// otel_metrics_* tables, so callers must gate on the infra-metrics module.
type NetworkEdgeHealth struct {
	Source            string
	Target            string
	RTTMs             float64
	FailedConnections uint64
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
}

// OverviewQuery filters TraceOverview.
type OverviewQuery struct {
	Tenant     string
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
	Range       TimeRange
	Service     string
	MinSeverity string // "", or a severity name (e.g. "ERROR") — matches >= its number
	Query       string // full-text substring on Body (case-insensitive)
	Limit       int
	Cursor      *LogCursor
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
	Tenant string
	Range  TimeRange
	Node   string // optional: only pods scheduled on this node
	Points int    // series buckets over Range (<=0 → backend default)
	Limit  int    // pods only: max rows (<=0 → backend default)
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
	Tenant string
	Window time.Duration // <=0 → backend default
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

// ErrorIssueQuery filters SearchErrorIssues. Zero values mean "no filter".
type ErrorIssueQuery struct {
	Tenant  string
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
	// NetworkEdges derives service→service edges from OBI network flow metrics
	// (otel_metrics_sum). It reads the metrics tables, so callers must gate it
	// on the infra-metrics module being active (the tables exist only then).
	NetworkEdges(ctx context.Context, q ServiceQuery) ([]ServiceEdge, error)
	// NetworkEdgeHealth returns per-edge RTT p95 + failed-connection counts from
	// OBI's TCP-stats metrics. Same infra-metrics gating as NetworkEdges.
	NetworkEdgeHealth(ctx context.Context, q ServiceQuery) ([]NetworkEdgeHealth, error)
	TraceOverview(ctx context.Context, q OverviewQuery) ([]OperationStats, error)
	SearchTraces(ctx context.Context, q TraceQuery) (TracePage, error)
	GetTrace(ctx context.Context, tenant, traceID string) (Trace, error)
	// FindSpanTrace resolves the trace containing spanID, or ErrNotFound.
	FindSpanTrace(ctx context.Context, tenant, spanID string) (traceID string, err error)
	TraceHeatmap(ctx context.Context, q HeatmapQuery) (Heatmap, error)
	SearchLogs(ctx context.Context, q LogQuery) (LogPage, error)
	LogsForTrace(ctx context.Context, tenant, traceID string) ([]LogRecord, error)
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
	GetErrorIssue(ctx context.Context, tenant string, fingerprint uint64) (ErrorIssue, error)
	ListErrorEvents(ctx context.Context, q ErrorEventQuery) (ErrorEventPage, error)
	ErrorIssueHistogram(ctx context.Context, tenant string, fingerprint uint64, r TimeRange, points int) ([]ErrorHistogramPoint, error)
	// SetErrorIssueStatus records a triage decision (unresolved|resolved|ignored).
	SetErrorIssueStatus(ctx context.Context, tenant string, fingerprint uint64, status string) error
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
	// Auth (core): local users, per-project grants, server-side sessions.
	// GetAuthUserByEmail/GetAuthUser return ErrNotFound for unknown users;
	// disabled users ARE returned (callers decide). SaveAuthUser upserts by
	// ID. ReplaceAuthGrants replaces the user's grant set (tombstone missing
	// scopes, upsert the rest). GetAuthSession returns ErrNotFound for
	// unknown, revoked or expired sessions. RevokeAuthSession likewise
	// returns ErrNotFound for a token that is unknown, already revoked, or
	// already expired.
	CountAuthUsers(ctx context.Context) (uint64, error)
	GetAuthUser(ctx context.Context, id string) (AuthUser, error)
	GetAuthUserByEmail(ctx context.Context, email string) (AuthUser, error)
	ListAuthUsers(ctx context.Context) ([]AuthUser, error)
	SaveAuthUser(ctx context.Context, u AuthUser) error
	ListAuthGrants(ctx context.Context, userID string) ([]AuthGrant, error)
	ReplaceAuthGrants(ctx context.Context, userID string, grants []AuthGrant) error
	CreateAuthSession(ctx context.Context, s AuthSession) error
	GetAuthSession(ctx context.Context, tokenHash string) (AuthSession, error)
	RevokeAuthSession(ctx context.Context, tokenHash string) error
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
	Tenant string
	Range  TimeRange
	Limit  int
}
