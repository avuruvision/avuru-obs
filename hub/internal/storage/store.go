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

// ServiceEdge is a caller→callee call edge derived from trace spans (a Client
// span and the cross-service Server span it spawned), with call volume.
type ServiceEdge struct {
	Source     string
	Target     string
	Count      uint64
	ErrorCount uint64
}

// OverviewQuery filters TraceOverview.
type OverviewQuery struct {
	Tenant     string
	Range      TimeRange
	Service    string // optional
	ExcludeAux bool   // drop health-check/metrics/control-plane traffic
}

// OperationStats aggregates RED metrics for one (service, operation) pair
// over root spans.
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

// Store is the telemetry query seam implemented by storage backends.
type Store interface {
	Ping(ctx context.Context) error
	SystemStats(ctx context.Context) (SystemStats, error)
	ListServices(ctx context.Context, q ServiceQuery) ([]ServiceStats, error)
	ServiceEdges(ctx context.Context, q ServiceQuery) ([]ServiceEdge, error)
	TraceOverview(ctx context.Context, q OverviewQuery) ([]OperationStats, error)
	SearchTraces(ctx context.Context, q TraceQuery) (TracePage, error)
	GetTrace(ctx context.Context, tenant, traceID string) (Trace, error)
	TraceHeatmap(ctx context.Context, q HeatmapQuery) (Heatmap, error)
	SearchLogs(ctx context.Context, q LogQuery) (LogPage, error)
	LogsForTrace(ctx context.Context, tenant, traceID string) ([]LogRecord, error)
}
