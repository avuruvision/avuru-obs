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
	Edges     []storage.ServiceEdge
	Ops       []storage.OperationStats
	Page      storage.TracePage
	Traces    map[string]storage.Trace
	Heat      storage.Heatmap
	LogPage   storage.LogPage
	TraceLogs map[string][]storage.LogRecord
	Stats     storage.SystemStats
	StatsErr  error
	Nodes     []storage.NodeStat
	Pods      []storage.PodStat
	RED       []storage.REDSeries
	Written   []storage.ProfileSample

	// Last*Query record the most recent inputs for asserting parameter parsing.
	LastTraceQuery   storage.TraceQuery
	LastServiceQuery storage.ServiceQuery
	LastLogQuery     storage.LogQuery
	LastInfraQuery   storage.InfraQuery
	LastREDQuery     storage.REDQuery
}

func (f *Fake) REDSeries(_ context.Context, q storage.REDQuery) ([]storage.REDSeries, error) {
	f.LastREDQuery = q
	return f.RED, nil
}

func (f *Fake) WriteProfileSamples(_ context.Context, samples []storage.ProfileSample) error {
	f.Written = append(f.Written, samples...)
	return nil
}

func (f *Fake) ListNodeStats(_ context.Context, q storage.InfraQuery) ([]storage.NodeStat, error) {
	f.LastInfraQuery = q
	return f.Nodes, nil
}

func (f *Fake) ListPodStats(_ context.Context, q storage.InfraQuery) ([]storage.PodStat, error) {
	f.LastInfraQuery = q
	return f.Pods, nil
}

var _ storage.Store = (*Fake)(nil)

func (f *Fake) Ping(context.Context) error { return f.PingErr }

func (f *Fake) SystemStats(context.Context) (storage.SystemStats, error) {
	return f.Stats, f.StatsErr
}

func (f *Fake) ListServices(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceStats, error) {
	f.LastServiceQuery = q
	return f.Services, nil
}

func (f *Fake) ServiceEdges(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	f.LastServiceQuery = q
	return f.Edges, nil
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
