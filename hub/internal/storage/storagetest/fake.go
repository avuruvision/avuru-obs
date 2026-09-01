// Package storagetest provides an in-memory storage.Store fake for handler
// tests (fakes over mocks, per agent_docs/go_style.md).
package storagetest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Fake implements storage.Store from canned data. Zero value is usable.
type Fake struct {
	PingErr  error
	Services []storage.ServiceStats
	Labels   []storage.ServiceLabel
	Edges    []storage.ServiceEdge
	// Collapsed is what CollapsedEdges returns, and LastCollapseTransport
	// records the classified transport set it was called with — the assertion
	// that the handler resolves the mesh BEFORE it queries edges.
	Collapsed             []storage.ServiceEdge
	LastCollapseTransport []string
	CollapseCalls         int
	NetEdges              []storage.ServiceEdge
	ControlPlane          storage.MeshControlPlane
	// Endpoint-check results: per-check history for the results route, and the
	// recent-states map the health evaluation's consecutive-failure rule reads.
	CheckResultsByID   map[string][]storage.CheckResult
	CheckStates        map[string][]storage.CheckResult
	RecordedChecks     []storage.CheckResult
	Virtual            []storage.VirtualTarget
	NetEdgeHealth      []storage.NetworkEdgeHealth
	Zones              []storage.ZoneTraffic
	Tags               []storage.TagKey
	Ops                []storage.OperationStats
	Breakdown          storage.Breakdown
	BreakdownErr       error
	LastBreakdownQuery storage.BreakdownQuery
	// AI observability: LastAIQuery records the filters the handler built, so
	// a test can assert the trace vocabulary reached storage.
	AIUsageResult storage.AIUsage
	AICallerRows  []storage.AICallerUsage
	AIToolRows    []storage.AIToolUsage
	AISpendRows   []storage.AIServiceSpend
	AIErr         error
	LastAIQuery   storage.AIQuery
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

	ServiceEnergies    []storage.ServiceEnergy
	NodeEnergies       []storage.NodeEnergy
	NodeCoverageResult storage.NodeCoverage
	GreenErr           error

	WorkloadCostRows []storage.WorkloadCost
	NodeCostRows     []storage.NodeCost
	CostErr          error
	LastCostQuery    storage.CostQuery

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

	// Collection overlay fake.
	Overlay       storage.CollectionOverlay
	OverlaySet    bool
	OverlayErr    error
	SavedOverlays []storage.CollectionOverlay

	// Auth fakes. Users is keyed by ID; Grants by user ID; Sessions by token
	// hash. UsersByEmail is a convenience index for tests that want a user
	// they only know the address of — GetAuthUserByEmail does NOT read it,
	// because one entry per address cannot represent two users sharing one.
	Users        map[string]storage.AuthUser
	UsersByEmail map[string]storage.AuthUser
	Grants       map[string][]storage.AuthGrant
	Sessions     map[string]storage.AuthSession
	SavedUsers   []storage.AuthUser

	// Project fakes. Projects keyed by ID; only live projects are ever stored
	// (DeleteProject removes the entry, mirroring the tombstone-then-FINAL read).
	// ProjectsErr forces ListProjects to fail (resolveTenants must fail closed).
	Projects        map[string]storage.Project
	ProjectsErr     error
	SavedProjects   []storage.Project
	DeletedProjects []string
	// Trims records every TrimTenant call in order — the retention trimmer is
	// asserted on which tenants it trimmed and to what cutoff, not on rows
	// disappearing. TrimErr forces the call to fail (one bad tenant must not
	// stop the sweep).
	// Per-project usage fakes. UsageTenants records the resolved tenant set the
	// handler asked for — the assertion that an aggregate reports its members.
	Usage        storage.TenantUsage
	UsageErr     error
	UsageTenants [][]string
	Trims        []TrimCall
	TrimErr      error
	TrimMu       sync.Mutex
	Trimmed      []string // tables TrimTenant reports on success

	// Service-group fakes, keyed by Name on the same live-rows-only rule.
	ServiceGroups        map[string]storage.ServiceGroup
	ServiceGroupsErr     error
	SavedServiceGroups   []storage.ServiceGroup
	DeletedServiceGroups []string

	// OIDC group-mapping fakes, keyed by Group on the same live-rows-only rule.
	OIDCGroupMappings            map[string]storage.OIDCGroupMapping
	OIDCGroupMappingsErr         error
	SavedOIDCGroupMappings       []storage.OIDCGroupMapping
	DeletedOIDCGroupMappings     []string
	ResetOIDCGroupMappingsCalled bool

	// Ingest-key fakes (auth Plan C). IngestKeys keyed by hash; only live keys
	// are ever stored (RevokeIngestKey removes the entry, mirroring the
	// tombstone-then-FINAL read).
	IngestKeys      map[string]storage.AuthIngestKey
	SavedIngestKeys []storage.AuthIngestKey
	RevokedKeys     []string

	// API-token fakes. Same shape as the ingest-key ones: AuthTokens is keyed
	// by hash and holds only live tokens, so RevokeAuthToken deletes the entry
	// rather than flagging it. AuthTokensErr forces a store failure, which the
	// middleware must surface as 503 rather than 401.
	AuthTokens      map[string]storage.AuthToken
	AuthTokensErr   error
	SavedAuthTokens []storage.AuthToken
	RevokedTokens   []string
	TouchedTokens   []string

	// Last*Query record the most recent inputs for asserting parameter parsing.
	LastTraceQuery        storage.TraceQuery
	LastServiceQuery      storage.ServiceQuery
	LastOverviewQuery     storage.OverviewQuery
	LastLogQuery          storage.LogQuery
	LastInfraQuery        storage.InfraQuery
	LastAgentQuery        storage.AgentQuery
	LastREDQuery          storage.REDQuery
	LastProfileQuery      storage.ProfileQuery
	LastIssueQuery        storage.ErrorIssueQuery
	LastEventQuery        storage.ErrorEventQuery
	LastGreenQuery        storage.GreenQuery
	LastSpanLookupTenants []string
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

func (f *Fake) CollapsedEdges(_ context.Context, q storage.ServiceQuery, transport []string) ([]storage.ServiceEdge, error) {
	f.LastServiceQuery = q
	f.LastCollapseTransport = transport
	f.CollapseCalls++
	if len(transport) == 0 {
		return nil, nil
	}
	return f.Collapsed, nil
}

func (f *Fake) RecordCheckResult(_ context.Context, r storage.CheckResult) error {
	f.RecordedChecks = append(f.RecordedChecks, r)
	return nil
}

func (f *Fake) CheckResults(_ context.Context, q storage.CheckQuery) ([]storage.CheckResult, error) {
	return f.CheckResultsByID[q.CheckID], nil
}

func (f *Fake) LatestCheckStates(_ context.Context, q storage.ServiceQuery, _ int) (map[string][]storage.CheckResult, error) {
	f.LastServiceQuery = q
	return f.CheckStates, nil
}

func (f *Fake) MeshControlPlane(_ context.Context, q storage.ServiceQuery) (storage.MeshControlPlane, error) {
	f.LastServiceQuery = q
	return f.ControlPlane, nil
}

func (f *Fake) TagKeys(_ context.Context, q storage.ServiceQuery) ([]storage.TagKey, error) {
	f.LastServiceQuery = q
	return f.Tags, nil
}

func (f *Fake) VirtualTargets(_ context.Context, q storage.ServiceQuery) ([]storage.VirtualTarget, error) {
	f.LastServiceQuery = q
	return f.Virtual, nil
}

func (f *Fake) NetworkEdges(_ context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	f.LastServiceQuery = q
	return f.NetEdges, nil
}

func (f *Fake) NetworkEdgeHealth(_ context.Context, q storage.ServiceQuery) ([]storage.NetworkEdgeHealth, error) {
	f.LastServiceQuery = q
	return f.NetEdgeHealth, nil
}

func (f *Fake) ZoneTraffic(_ context.Context, q storage.ServiceQuery) ([]storage.ZoneTraffic, error) {
	f.LastServiceQuery = q
	return f.Zones, nil
}

func (f *Fake) TraceOverview(_ context.Context, q storage.OverviewQuery) ([]storage.OperationStats, error) {
	f.LastOverviewQuery = q
	return f.Ops, nil
}

func (f *Fake) TraceBreakdown(_ context.Context, q storage.BreakdownQuery) (storage.Breakdown, error) {
	f.LastBreakdownQuery = q
	return f.Breakdown, f.BreakdownErr
}

func (f *Fake) SearchTraces(_ context.Context, q storage.TraceQuery) (storage.TracePage, error) {
	f.LastTraceQuery = q
	return f.Page, nil
}

func (f *Fake) GetTrace(_ context.Context, _ []string, traceID string) (storage.Trace, error) {
	t, ok := f.Traces[traceID]
	if !ok {
		return storage.Trace{}, storage.ErrNotFound
	}
	return t, nil
}

func (f *Fake) FindSpanTrace(_ context.Context, tenants []string, spanID string) (string, error) {
	f.LastSpanLookupTenants = tenants
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

func (f *Fake) LogsForTrace(_ context.Context, _ []string, traceID string) ([]storage.LogRecord, error) {
	return f.TraceLogs[traceID], nil
}

func (f *Fake) SearchErrorIssues(_ context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error) {
	f.LastIssueQuery = q
	return f.Issues, nil
}

func (f *Fake) GetErrorIssue(_ context.Context, _ []string, _ uint64) (storage.ErrorIssue, error) {
	return f.Issue, f.IssueErr
}

func (f *Fake) ListErrorEvents(_ context.Context, q storage.ErrorEventQuery) (storage.ErrorEventPage, error) {
	f.LastEventQuery = q
	return f.EventPage, nil
}

func (f *Fake) ErrorIssueHistogram(_ context.Context, _ []string, _ uint64, _ storage.TimeRange, _ int) ([]storage.ErrorHistogramPoint, error) {
	return f.Histogram, nil
}

func (f *Fake) SetErrorIssueStatus(_ context.Context, tenant string, fingerprint uint64, status string) error {
	f.StatusWrites = append(f.StatusWrites, StatusWrite{Tenant: tenant, Fingerprint: fingerprint, Status: status})
	return nil
}

func (f *Fake) ServiceEnergy(_ context.Context, q storage.GreenQuery) ([]storage.ServiceEnergy, error) {
	f.LastGreenQuery = q
	if f.GreenErr != nil {
		return nil, f.GreenErr
	}
	return f.ServiceEnergies, nil
}

func (f *Fake) NodeEnergy(_ context.Context, q storage.GreenQuery) ([]storage.NodeEnergy, error) {
	f.LastGreenQuery = q
	if f.GreenErr != nil {
		return nil, f.GreenErr
	}
	return f.NodeEnergies, nil
}

func (f *Fake) NodeCoverage(_ context.Context, q storage.GreenQuery) (storage.NodeCoverage, error) {
	f.LastGreenQuery = q
	if f.GreenErr != nil {
		return storage.NodeCoverage{}, f.GreenErr
	}
	return f.NodeCoverageResult, nil
}

// Cost (module cost).

func (f *Fake) WorkloadCosts(_ context.Context, q storage.CostQuery) ([]storage.WorkloadCost, error) {
	f.LastCostQuery = q
	if f.CostErr != nil {
		return nil, f.CostErr
	}
	return f.WorkloadCostRows, nil
}

func (f *Fake) NodeCosts(_ context.Context, q storage.CostQuery) ([]storage.NodeCost, error) {
	f.LastCostQuery = q
	if f.CostErr != nil {
		return nil, f.CostErr
	}
	return f.NodeCostRows, nil
}

// AI observability (module ai).

func (f *Fake) AIModels(_ context.Context, q storage.AIQuery) (storage.AIUsage, error) {
	f.LastAIQuery = q
	if f.AIErr != nil {
		return storage.AIUsage{}, f.AIErr
	}
	return f.AIUsageResult, nil
}

func (f *Fake) AICallers(_ context.Context, q storage.AIQuery) ([]storage.AICallerUsage, error) {
	f.LastAIQuery = q
	if f.AIErr != nil {
		return nil, f.AIErr
	}
	return f.AICallerRows, nil
}

func (f *Fake) AITools(_ context.Context, q storage.AIQuery) ([]storage.AIToolUsage, error) {
	f.LastAIQuery = q
	if f.AIErr != nil {
		return nil, f.AIErr
	}
	return f.AIToolRows, nil
}

func (f *Fake) AISpendByService(_ context.Context, q storage.AIQuery) ([]storage.AIServiceSpend, error) {
	f.LastAIQuery = q
	if f.AIErr != nil {
		return nil, f.AIErr
	}
	return f.AISpendRows, nil
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

func (f *Fake) LoadCollectionOverlay(_ context.Context) (storage.CollectionOverlay, error) {
	if f.OverlayErr != nil {
		return storage.CollectionOverlay{}, f.OverlayErr
	}
	if !f.OverlaySet {
		return storage.CollectionOverlay{}, storage.ErrNotFound
	}
	return f.Overlay, nil
}

func (f *Fake) SaveCollectionOverlay(_ context.Context, ov storage.CollectionOverlay) error {
	f.SavedOverlays = append(f.SavedOverlays, ov)
	f.Overlay = ov
	f.OverlaySet = true
	return nil
}

func (f *Fake) GetAuthUser(_ context.Context, id string) (storage.AuthUser, error) {
	u, ok := f.Users[id]
	if !ok {
		return storage.AuthUser{}, storage.ErrNotFound
	}
	return u, nil
}

// GetAuthUserByEmail mirrors the real store's local-first, lowest-Id ordering
// (clickhouse/auth.go). It scans Users rather than reading UsersByEmail: email
// is not unique — an SSO login can add a second row sharing a local user's
// address — and a map keyed by email cannot represent that at all, so it would
// silently drop one of the two and hide exactly the collision this ordering
// exists to resolve.
func (f *Fake) GetAuthUserByEmail(_ context.Context, email string) (storage.AuthUser, error) {
	var out []storage.AuthUser
	for _, u := range f.Users {
		if u.Email == email {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return storage.AuthUser{}, storage.ErrNotFound
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i].Origin == "local", out[j].Origin == "local"
		if li != lj {
			return li
		}
		return out[i].ID < out[j].ID
	})
	return out[0], nil
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

// DeleteAuthUser removes the user from both indexes — mirrors the tombstone
// from the caller's point of view (no read path can observe it afterwards).
// Returns ErrNotFound for an unknown or already-deleted id, matching
// DeleteProject.
func (f *Fake) DeleteAuthUser(_ context.Context, id string) error {
	u, ok := f.Users[id]
	if !ok {
		return storage.ErrNotFound
	}
	delete(f.UsersByEmail, u.Email)
	delete(f.Users, id)
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

// ListProjects returns live projects ordered by ID, matching the real
// ClickHouse implementation's ORDER BY (map iteration order is random).
func (f *Fake) ListProjects(context.Context) ([]storage.Project, error) {
	if f.ProjectsErr != nil {
		return nil, f.ProjectsErr
	}
	out := make([]storage.Project, 0, len(f.Projects))
	for _, p := range f.Projects {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Fake) GetProject(_ context.Context, id string) (storage.Project, error) {
	p, ok := f.Projects[id]
	if !ok {
		return storage.Project{}, storage.ErrNotFound
	}
	return p, nil
}

// SaveProject mirrors the ReplacingMergeTree upsert-by-ID.
func (f *Fake) SaveProject(_ context.Context, p storage.Project) error {
	if f.Projects == nil {
		f.Projects = make(map[string]storage.Project)
	}
	f.Projects[p.ID] = p
	f.SavedProjects = append(f.SavedProjects, p)
	return nil
}

// DeleteProject mirrors the tombstone from the caller's point of view: only
// live projects are ever observable, so a deleted id simply disappears.
func (f *Fake) DeleteProject(_ context.Context, id string) error {
	f.DeletedProjects = append(f.DeletedProjects, id)
	if _, ok := f.Projects[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.Projects, id)
	return nil
}

// TenantUsage returns the canned per-project usage and records the resolved
// tenant set it was asked for. The fake holds no telemetry, so a test states
// the answer rather than seeding rows to derive it; what it CAN assert is that
// the handler resolved an aggregate to its members before asking.
func (f *Fake) TenantUsage(_ context.Context, tenants []string, _ time.Time) (storage.TenantUsage, error) {
	f.TrimMu.Lock()
	defer f.TrimMu.Unlock()
	f.UsageTenants = append(f.UsageTenants, tenants)
	if f.UsageErr != nil {
		return storage.TenantUsage{}, f.UsageErr
	}
	return f.Usage, nil
}

// TrimCall is one recorded TrimTenant invocation.
type TrimCall struct {
	Tenant string
	Cutoff time.Time
}

// TrimTenant records the call. The fake holds no telemetry, so there is
// nothing to delete: what tests need is which tenant was trimmed and to what
// cutoff, which is exactly what the trimmer decides.
func (f *Fake) TrimTenant(_ context.Context, tenant string, cutoff time.Time) ([]string, error) {
	f.TrimMu.Lock()
	defer f.TrimMu.Unlock()
	f.Trims = append(f.Trims, TrimCall{Tenant: tenant, Cutoff: cutoff})
	if f.TrimErr != nil {
		return nil, f.TrimErr
	}
	return f.Trimmed, nil
}

// ListServiceGroups returns live groups ordered by Name, matching the real
// ClickHouse implementation's ORDER BY (map iteration order is random).
func (f *Fake) ListServiceGroups(context.Context) ([]storage.ServiceGroup, error) {
	if f.ServiceGroupsErr != nil {
		return nil, f.ServiceGroupsErr
	}
	out := make([]storage.ServiceGroup, 0, len(f.ServiceGroups))
	for _, g := range f.ServiceGroups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SaveServiceGroup mirrors the ReplacingMergeTree upsert-by-Name.
func (f *Fake) SaveServiceGroup(_ context.Context, g storage.ServiceGroup) error {
	if f.ServiceGroups == nil {
		f.ServiceGroups = make(map[string]storage.ServiceGroup)
	}
	f.ServiceGroups[g.Name] = g
	f.SavedServiceGroups = append(f.SavedServiceGroups, g)
	return nil
}

// DeleteServiceGroup mirrors the tombstone from the caller's point of view:
// only live groups are ever observable, so a deleted name simply disappears.
func (f *Fake) DeleteServiceGroup(_ context.Context, name string) error {
	f.DeletedServiceGroups = append(f.DeletedServiceGroups, name)
	if _, ok := f.ServiceGroups[name]; !ok {
		return storage.ErrNotFound
	}
	delete(f.ServiceGroups, name)
	return nil
}

// ListOIDCGroupMappings returns live rules ordered by Group, matching the
// real ClickHouse implementation's ORDER BY (map iteration order is random).
func (f *Fake) ListOIDCGroupMappings(context.Context) ([]storage.OIDCGroupMapping, error) {
	if f.OIDCGroupMappingsErr != nil {
		return nil, f.OIDCGroupMappingsErr
	}
	out := make([]storage.OIDCGroupMapping, 0, len(f.OIDCGroupMappings))
	for _, m := range f.OIDCGroupMappings {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out, nil
}

// SaveOIDCGroupMapping mirrors the ReplacingMergeTree upsert-by-Group.
func (f *Fake) SaveOIDCGroupMapping(_ context.Context, m storage.OIDCGroupMapping) error {
	if f.OIDCGroupMappings == nil {
		f.OIDCGroupMappings = make(map[string]storage.OIDCGroupMapping)
	}
	f.OIDCGroupMappings[m.Group] = m
	f.SavedOIDCGroupMappings = append(f.SavedOIDCGroupMappings, m)
	return nil
}

// DeleteOIDCGroupMapping mirrors the tombstone from the caller's point of
// view: only live rules are ever observable, so a deleted group simply
// disappears.
func (f *Fake) DeleteOIDCGroupMapping(_ context.Context, group string) error {
	f.DeletedOIDCGroupMappings = append(f.DeletedOIDCGroupMappings, group)
	if _, ok := f.OIDCGroupMappings[group]; !ok {
		return storage.ErrNotFound
	}
	delete(f.OIDCGroupMappings, group)
	return nil
}

// ResetOIDCGroupMappings mirrors tombstoning every live rule: for a fake that
// only ever tracks live rows, that is equivalent to clearing the map.
func (f *Fake) ResetOIDCGroupMappings(_ context.Context) error {
	f.ResetOIDCGroupMappingsCalled = true
	f.OIDCGroupMappings = nil
	return nil
}

// CreateIngestKey mirrors the ReplacingMergeTree insert (upsert by hash).
func (f *Fake) CreateIngestKey(_ context.Context, k storage.AuthIngestKey) error {
	if f.IngestKeys == nil {
		f.IngestKeys = make(map[string]storage.AuthIngestKey)
	}
	f.IngestKeys[k.KeyHash] = k
	f.SavedIngestKeys = append(f.SavedIngestKeys, k)
	return nil
}

// GetIngestKeyByHash returns a live key or ErrNotFound (unknown or revoked —
// revoked keys are removed from the map, mirroring the FINAL/Revoked read).
func (f *Fake) GetIngestKeyByHash(_ context.Context, keyHash string) (storage.AuthIngestKey, error) {
	k, ok := f.IngestKeys[keyHash]
	if !ok {
		return storage.AuthIngestKey{}, storage.ErrNotFound
	}
	return k, nil
}

// ListIngestKeys returns the live keys for one project, newest first (matching
// the real ORDER BY CreatedAt DESC — map iteration order is random).
func (f *Fake) ListIngestKeys(_ context.Context, project string) ([]storage.AuthIngestKey, error) {
	out := make([]storage.AuthIngestKey, 0, len(f.IngestKeys))
	for _, k := range f.IngestKeys {
		if k.Project == project {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].KeyHash < out[j].KeyHash
	})
	return out, nil
}

// RevokeIngestKey mirrors the tombstone: only live keys are observable, so a
// revoked hash simply disappears. ErrNotFound when no live key in the project
// has that hash.
func (f *Fake) RevokeIngestKey(_ context.Context, project, keyHash string) error {
	f.RevokedKeys = append(f.RevokedKeys, keyHash)
	k, ok := f.IngestKeys[keyHash]
	if !ok || k.Project != project {
		return storage.ErrNotFound
	}
	delete(f.IngestKeys, keyHash)
	return nil
}

// CreateAuthToken mirrors the ReplacingMergeTree insert (upsert by hash).
func (f *Fake) CreateAuthToken(_ context.Context, t storage.AuthToken) error {
	if f.AuthTokensErr != nil {
		return f.AuthTokensErr
	}
	if f.AuthTokens == nil {
		f.AuthTokens = make(map[string]storage.AuthToken)
	}
	f.AuthTokens[t.TokenHash] = t
	f.SavedAuthTokens = append(f.SavedAuthTokens, t)
	return nil
}

// GetAuthTokenByHash returns a live token or ErrNotFound (unknown or revoked —
// revoked tokens are removed from the map, mirroring the FINAL/Revoked read).
// An EXPIRED token is returned, exactly as the real store does: expiry is the
// auth layer's call, not storage's. AuthTokensErr takes precedence so a test
// can distinguish "bad credential" from "store down" — the difference between
// a 401 and a 503.
func (f *Fake) GetAuthTokenByHash(_ context.Context, tokenHash string) (storage.AuthToken, error) {
	if f.AuthTokensErr != nil {
		return storage.AuthToken{}, f.AuthTokensErr
	}
	t, ok := f.AuthTokens[tokenHash]
	if !ok {
		return storage.AuthToken{}, storage.ErrNotFound
	}
	return t, nil
}

// ListAuthTokens returns one user's live tokens, newest first (matching the real
// ORDER BY CreatedAt DESC — map iteration order is random), expired ones
// included.
func (f *Fake) ListAuthTokens(_ context.Context, userID string) ([]storage.AuthToken, error) {
	if f.AuthTokensErr != nil {
		return nil, f.AuthTokensErr
	}
	out := make([]storage.AuthToken, 0, len(f.AuthTokens))
	for _, t := range f.AuthTokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].TokenHash < out[j].TokenHash
	})
	return out, nil
}

// RevokeAuthToken mirrors the tombstone: only live tokens are observable, so a
// revoked hash simply disappears. ErrNotFound when no live token with that hash
// belongs to that user.
func (f *Fake) RevokeAuthToken(_ context.Context, userID, tokenHash string) error {
	if f.AuthTokensErr != nil {
		return f.AuthTokensErr
	}
	f.RevokedTokens = append(f.RevokedTokens, tokenHash)
	t, ok := f.AuthTokens[tokenHash]
	if !ok || t.UserID != userID {
		return storage.ErrNotFound
	}
	delete(f.AuthTokens, tokenHash)
	return nil
}

// TouchAuthToken records the use and updates LastUsedAt in place, keeping every
// other field — the real store re-inserts the whole row for the same reason.
func (f *Fake) TouchAuthToken(_ context.Context, tokenHash string, at time.Time) error {
	if f.AuthTokensErr != nil {
		return f.AuthTokensErr
	}
	f.TouchedTokens = append(f.TouchedTokens, tokenHash)
	t, ok := f.AuthTokens[tokenHash]
	if !ok {
		return storage.ErrNotFound
	}
	t.LastUsedAt = at
	f.AuthTokens[tokenHash] = t
	return nil
}
