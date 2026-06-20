// Package storagetest provides an in-memory storage.Store fake for handler
// tests (fakes over mocks, per agent_docs/go_style.md).
package storagetest

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Fake implements storage.Store from canned data. Zero value is usable.
type Fake struct {
	PingErr   error
	Services  []storage.ServiceStats
	Ops       []storage.OperationStats
	Page      storage.TracePage
	Traces    map[string]storage.Trace
	Heat      storage.Heatmap
	LogPage   storage.LogPage
	TraceLogs map[string][]storage.LogRecord

	// LastTraceQuery / LastLogQuery record the most recent inputs for
	// asserting parameter parsing.
	LastTraceQuery storage.TraceQuery
	LastLogQuery   storage.LogQuery
}

var _ storage.Store = (*Fake)(nil)

func (f *Fake) Ping(context.Context) error { return f.PingErr }

func (f *Fake) ListServices(_ context.Context, _ storage.ServiceQuery) ([]storage.ServiceStats, error) {
	return f.Services, nil
}

func (f *Fake) TraceOverview(_ context.Context, _ storage.OverviewQuery) ([]storage.OperationStats, error) {
	return f.Ops, nil
}

func (f *Fake) SearchTraces(_ context.Context, q storage.TraceQuery) (storage.TracePage, error) {
	f.LastTraceQuery = q
	return f.Page, nil
}

func (f *Fake) GetTrace(_ context.Context, _, traceID string) (storage.Trace, error) {
	t, ok := f.Traces[traceID]
	if !ok {
		return storage.Trace{}, storage.ErrNotFound
	}
	return t, nil
}

func (f *Fake) TraceHeatmap(_ context.Context, _ storage.HeatmapQuery) (storage.Heatmap, error) {
	return f.Heat, nil
}

func (f *Fake) SearchLogs(_ context.Context, q storage.LogQuery) (storage.LogPage, error) {
	f.LastLogQuery = q
	return f.LogPage, nil
}

func (f *Fake) LogsForTrace(_ context.Context, _, traceID string) ([]storage.LogRecord, error) {
	return f.TraceLogs[traceID], nil
}
