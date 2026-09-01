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
	SearchTraces(ctx context.Context, q storage.TraceQuery) (storage.TracePage, error)
	GetTrace(ctx context.Context, tenants []string, traceID string) (storage.Trace, error)
	SearchLogs(ctx context.Context, q storage.LogQuery) (storage.LogPage, error)
	LogsForTrace(ctx context.Context, tenants []string, traceID string) ([]storage.LogRecord, error)
	SearchErrorIssues(ctx context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error)
	LoadAlertStates(ctx context.Context, tenant string) ([]storage.AlertState, error)
}
