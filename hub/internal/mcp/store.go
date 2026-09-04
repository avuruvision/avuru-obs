package mcp

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Store is the slice of storage.Store the tools read. Every method here is one
// the REST handlers already call, which is the point: the two surfaces answer
// from the same reads, so they cannot drift into reporting different numbers
// for the same window.
type Store interface {
	ListServices(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceStats, error)
	ServiceEdges(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error)
	// CollapsedEdges recovers the app→app dependencies a service mesh hides,
	// by walking each trace's ancestry across the proxies named in `transport`.
	// The service map has read it since v0.9; service_context reads the same
	// rows so the two surfaces describe a meshed estate identically.
	CollapsedEdges(ctx context.Context, q storage.ServiceQuery, transport []string) ([]storage.ServiceEdge, error)
	SearchTraces(ctx context.Context, q storage.TraceQuery) (storage.TracePage, error)
	GetTrace(ctx context.Context, tenants []string, traceID string) (storage.Trace, error)
	SearchLogs(ctx context.Context, q storage.LogQuery) (storage.LogPage, error)
	LogsForTrace(ctx context.Context, tenants []string, traceID string) ([]storage.LogRecord, error)
	SearchErrorIssues(ctx context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error)
	LoadAlertStates(ctx context.Context, tenant string) ([]storage.AlertState, error)
}
