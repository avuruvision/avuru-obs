// Package storagetest provides an in-memory storage.Store fake for handler
// tests (fakes over mocks, per agent_docs/go_style.md).
package storagetest

import (
	"context"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Fake implements storage.Store from canned data. Zero value is usable.
type Fake struct {
	PingErr       error
	Services      []storage.ServiceStats
	Labels        []storage.ServiceLabel
	Edges         []storage.ServiceEdge
	NetEdges      []storage.ServiceEdge
	NetEdgeHealth []storage.NetworkEdgeHealth
	Ops           []storage.OperationStats
	Page          storage.TracePage
	Traces        map[string]storage.Trace
	SpanTraces    map[string]string // spanId -> traceId
	Heat          storage.Heatmap
	LogPage       storage.LogPage
	TraceLogs     map[string][]storage.LogRecord
	Stats         storage.SystemStats
	StatsErr      error
	Nodes         []storage.NodeStat
	Pods          []storage.PodStat
	Agents        []storage.AgentNode
	Tenants       []string
	TenantsErr    error
	RED           []storage.REDSeries
	Written       []storage.ProfileSample
	Profiled      []storage.ProfiledService
	Flame         storage.FlameNode

	Issues       []storage.ErrorIssue
	Issue        storage.ErrorIssue
	IssueErr     error
	EventPage    storage.ErrorEventPage
	Histogram    []storage.ErrorHistogramPoint
	StatusWrites []StatusWrite

	// Alerting fakes.
	AlertStates           []storage.AlertState
	AlertHistoryRows      []storage.AlertHistoryEntry
	SavedAlertStates      [][]storage.AlertState
	AppendedHistory       [][]storage.AlertHistoryEntry
	LastAlertHistoryQuery storage.AlertHistoryQuery
	Channels              []storage.AlertChannel
	ChannelsErr           error
	SavedChannels         []storage.AlertChannel
	DeletedChannels       []string

	// Auth fakes. Users is keyed by ID, UsersByEmail by email (both hold the
	// same values); Grants by user ID; Sessions by token hash.
	Users        map[string]storage.AuthUser
	UsersByEmail map[string]storage.AuthUser
	Grants       map[string][]storage.AuthGrant
	Sessions     map[string]storage.AuthSession
	SavedUsers   []storage.AuthUser

	// Last*Query record the most recent inputs for asserting parameter parsing.
	LastTraceQuery       storage.TraceQuery
	LastServiceQuery     storage.ServiceQuery
	LastOverviewQuery    storage.OverviewQuery
	LastLogQuery         storage.LogQuery
	LastInfraQuery       storage.InfraQuery
	LastAgentQuery       storage.AgentQuery
	LastREDQuery         storage.REDQuery
	LastProfileQuery     storage.ProfileQuery
	LastIssueQuery       storage.ErrorIssueQuery
	LastEventQuery       storage.ErrorEventQuery
	LastSpanLookupTenant string
}

// StatusWrite records a SetErrorIssueStatus call.
type StatusWrite struct {
	Tenant      string
	Fingerprint uint64
	Status      string
}

func (f *Fake) REDSeries(_ context.Context, q storage.REDQuery) ([]storage.REDSeries, error) {
	f.LastREDQuery = q
	return f.RED, nil
}

func (f *Fake) WriteProfileSamples(_ context.Context, samples []storage.ProfileSample) error {
	f.Written = append(f.Written, samples...)
	return nil
}

func (f *Fake) ListProfiledServices(_ context.Context, q storage.ProfileQuery) ([]storage.ProfiledService, error) {
	f.LastProfileQuery = q
	return f.Profiled, nil
}

func (f *Fake) ProfileFlamegraph(_ context.Context, q storage.ProfileQuery) (storage.FlameNode, error) {
	f.LastProfileQuery = q
	return f.Flame, nil
}

func (f *Fake) ListNodeStats(_ context.Context, q storage.InfraQuery) ([]storage.NodeStat, error) {
	f.LastInfraQuery = q
	return f.Nodes, nil
}

func (f *Fake) ListPodStats(_ context.Context, q storage.InfraQuery) ([]storage.PodStat, error) {
	f.LastInfraQuery = q
	return f.Pods, nil
}

func (f *Fake) ListAgentNodes(_ context.Context, q storage.AgentQuery) ([]storage.AgentNode, error) {
	f.LastAgentQuery = q
	return f.Agents, nil
}

func (f *Fake) ListTenants(context.Context) ([]string, error) {
	return f.Tenants, f.TenantsErr
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

func (f *Fake) ServiceLabels(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceLabel, error) {
	f.LastServiceQuery = q
	return f.Labels, nil
}

func (f *Fake) ServiceEdges(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	f.LastServiceQuery = q
	return f.Edges, nil
}

func (f *Fake) NetworkEdges(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	f.LastServiceQuery = q
	return f.NetEdges, nil
}

func (f *Fake) NetworkEdgeHealth(_ context.Context, q storage.ServiceQuery) ([]storage.NetworkEdgeHealth, error) {
	f.LastServiceQuery = q
	return f.NetEdgeHealth, nil
}

func (f *Fake) TraceOverview(_ context.Context, q storage.OverviewQuery) ([]storage.OperationStats, error) {
	f.LastOverviewQuery = q
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

func (f *Fake) FindSpanTrace(_ context.Context, tenant, spanID string) (string, error) {
	f.LastSpanLookupTenant = tenant
	if id, ok := f.SpanTraces[spanID]; ok {
		return id, nil
	}
	return "", storage.ErrNotFound
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

func (f *Fake) SearchErrorIssues(_ context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error) {
	f.LastIssueQuery = q
	return f.Issues, nil
}

func (f *Fake) GetErrorIssue(_ context.Context, _ string, _ uint64) (storage.ErrorIssue, error) {
	return f.Issue, f.IssueErr
}

func (f *Fake) ListErrorEvents(_ context.Context, q storage.ErrorEventQuery) (storage.ErrorEventPage, error) {
	f.LastEventQuery = q
	return f.EventPage, nil
}

func (f *Fake) ErrorIssueHistogram(_ context.Context, _ string, _ uint64, _ storage.TimeRange, _ int) ([]storage.ErrorHistogramPoint, error) {
	return f.Histogram, nil
}

func (f *Fake) SetErrorIssueStatus(_ context.Context, tenant string, fingerprint uint64, status string) error {
	f.StatusWrites = append(f.StatusWrites, StatusWrite{Tenant: tenant, Fingerprint: fingerprint, Status: status})
	return nil
}

func (f *Fake) LoadAlertStates(_ context.Context, tenant string) ([]storage.AlertState, error) {
	var out []storage.AlertState
	for _, s := range f.AlertStates {
		if s.Tenant == tenant {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *Fake) SaveAlertStates(_ context.Context, states []storage.AlertState) error {
	f.SavedAlertStates = append(f.SavedAlertStates, states)
	return nil
}

func (f *Fake) AppendAlertHistory(_ context.Context, entries []storage.AlertHistoryEntry) error {
	f.AppendedHistory = append(f.AppendedHistory, entries)
	return nil
}

func (f *Fake) ListAlertHistory(_ context.Context, q storage.AlertHistoryQuery) ([]storage.AlertHistoryEntry, error) {
	f.LastAlertHistoryQuery = q
	return f.AlertHistoryRows, nil
}

func (f *Fake) ListAlertChannels(_ context.Context) ([]storage.AlertChannel, error) {
	if f.ChannelsErr != nil {
		return nil, f.ChannelsErr
	}
	return f.Channels, nil
}

// SaveAlertChannel mirrors the ReplacingMergeTree upsert: replace by name or
// append, so ListAlertChannels reflects the write within a test.
func (f *Fake) SaveAlertChannel(_ context.Context, ch storage.AlertChannel) error {
	f.SavedChannels = append(f.SavedChannels, ch)
	for i := range f.Channels {
		if f.Channels[i].Name == ch.Name {
			f.Channels[i] = ch
			return nil
		}
	}
	f.Channels = append(f.Channels, ch)
	return nil
}

func (f *Fake) DeleteAlertChannel(_ context.Context, name string) error {
	f.DeletedChannels = append(f.DeletedChannels, name)
	for i := range f.Channels {
		if f.Channels[i].Name == name {
			f.Channels = append(f.Channels[:i], f.Channels[i+1:]...)
			return nil
		}
	}
	return storage.ErrNotFound
}

func (f *Fake) CountAuthUsers(context.Context) (uint64, error) {
	return uint64(len(f.Users)), nil
}

func (f *Fake) GetAuthUser(_ context.Context, id string) (storage.AuthUser, error) {
	u, ok := f.Users[id]
	if !ok {
		return storage.AuthUser{}, storage.ErrNotFound
	}
	return u, nil
}

func (f *Fake) GetAuthUserByEmail(_ context.Context, email string) (storage.AuthUser, error) {
	u, ok := f.UsersByEmail[email]
	if !ok {
		return storage.AuthUser{}, storage.ErrNotFound
	}
	return u, nil
}

// ListAuthUsers returns all users ordered by Email, matching the real
// ClickHouse implementation's ORDER BY — map iteration order is random, and
// handler tests that don't sort themselves would otherwise be flaky.
func (f *Fake) ListAuthUsers(context.Context) ([]storage.AuthUser, error) {
	out := make([]storage.AuthUser, 0, len(f.Users))
	for _, u := range f.Users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

// SaveAuthUser mirrors the ReplacingMergeTree upsert-by-Id: it updates both
// maps (keyed by ID and by email) and records the write in SavedUsers. If the
// user's email changed since the last save, the stale UsersByEmail entry is
// removed so a lookup by the old address correctly misses.
func (f *Fake) SaveAuthUser(_ context.Context, u storage.AuthUser) error {
	if f.Users == nil {
		f.Users = make(map[string]storage.AuthUser)
	}
	if f.UsersByEmail == nil {
		f.UsersByEmail = make(map[string]storage.AuthUser)
	}
	if old, ok := f.Users[u.ID]; ok && old.Email != u.Email {
		delete(f.UsersByEmail, old.Email)
	}
	f.Users[u.ID] = u
	f.UsersByEmail[u.Email] = u
	f.SavedUsers = append(f.SavedUsers, u)
	return nil
}

// ListAuthGrants returns a Scope-sorted copy of the user's grants (matching
// the real implementation's ORDER BY), so callers can't mutate the fake's
// backing slice through the returned value.
func (f *Fake) ListAuthGrants(_ context.Context, userID string) ([]storage.AuthGrant, error) {
	src := f.Grants[userID]
	out := make([]storage.AuthGrant, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, nil
}

// ReplaceAuthGrants overwrites the user's grant slice wholesale, mirroring the
// tombstone-then-write semantics of the ClickHouse implementation from the
// caller's point of view (only the live set is ever visible).
func (f *Fake) ReplaceAuthGrants(_ context.Context, userID string, grants []storage.AuthGrant) error {
	if f.Grants == nil {
		f.Grants = make(map[string][]storage.AuthGrant)
	}
	f.Grants[userID] = grants
	return nil
}

func (f *Fake) CreateAuthSession(_ context.Context, s storage.AuthSession) error {
	if f.Sessions == nil {
		f.Sessions = make(map[string]storage.AuthSession)
	}
	f.Sessions[s.TokenHash] = s
	return nil
}

func (f *Fake) GetAuthSession(_ context.Context, tokenHash string) (storage.AuthSession, error) {
	s, ok := f.Sessions[tokenHash]
	if !ok || time.Now().After(s.ExpiresAt) {
		return storage.AuthSession{}, storage.ErrNotFound
	}
	return s, nil
}

func (f *Fake) RevokeAuthSession(_ context.Context, tokenHash string) error {
	if _, ok := f.Sessions[tokenHash]; !ok {
		return storage.ErrNotFound
	}
	delete(f.Sessions, tokenHash)
	return nil
}

// RevokeAuthSessionsForUser deletes every session belonging to userID —
// mirrors the ClickHouse tombstone-write from the caller's point of view
// (only live sessions are ever observable via GetAuthSession), without
// needing to model the revoked-but-still-present row.
func (f *Fake) RevokeAuthSessionsForUser(_ context.Context, userID string) error {
	for hash, s := range f.Sessions {
		if s.UserID == userID {
			delete(f.Sessions, hash)
		}
	}
	return nil
}
