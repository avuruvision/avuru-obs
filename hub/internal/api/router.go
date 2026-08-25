// Package api owns the hub's HTTP surface: route registration and handlers.
// Business logic belongs in the service/storage layers, not here.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/collection"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

// Version is the hub build version, overridden at link time via
// -ldflags "-X github.com/avuru/avuru-obs/hub/internal/api.Version=...".
var Version = "dev"

// StoreProvider returns the current telemetry store, or nil while the
// backend is unreachable (signal endpoints then answer 503; /healthz stays
// 200 so the pod isn't killed for a ClickHouse outage).
type StoreProvider func() storage.Store

// Config holds non-store handler settings (e.g. reported retention).
type Config struct {
	RetentionTracesDays   int
	RetentionLogsDays     int
	RetentionMetricsDays  int
	RetentionProfilesDays int
	// Projects declared through deployment config (AVURUOBS_PROJECTS) —
	// merged with data-observed tenants by GET /api/v1/projects.
	Projects []string
	// Modules is the active-module set (AVURUOBS_MODULES); nil means all
	// modules — the backward-compatible default. Routes owned by an inactive
	// module are not registered (404).
	Modules modules.Set
	// Groups resolves the current service-health configuration: chart-declared
	// groups (hot-reloaded from the mounted ConfigMap) merged with the ones
	// authored here. It is the SAME resolver main hands to the alerting
	// evaluator — a second merge point would let a UI-created group show on
	// /health and never page (design/2026-08-07-service-groups-crud.md).
	// nil → health.Default().
	Groups *health.Resolver
	// AlertsConfig returns the current alerting configuration (hot-reloaded).
	// Read-only endpoint use only; the evaluator lives in cmd/hub. nil →
	// alerting.Default().
	AlertsConfig func() alerting.Config
	// SchemaStatus reports whether the ClickHouse schema this install expects
	// has actually been applied — an accessor, like the configs above, because
	// cmd/hub's schema gate updates it in the background. nil → the Settings →
	// Status view simply omits the component.
	SchemaStatus func() storage.SchemaStatus
	// Notifier delivers channel test notifications (POST
	// /api/v1/alerts/channels/{name}/test). Shared with the evaluator so the
	// SSRF policy is identical. nil → test endpoint answers 503.
	Notifier alerting.Notifier
	// Auth enables authentication when non-nil; nil keeps the pre-auth open
	// behavior (auth.enabled=false).
	Auth *auth.Service
	// AnonymousIdentity, when non-nil, is served to requests without a valid
	// session (demo mode: a Viewer scoped to listed projects).
	AnonymousIdentity *auth.Identity
	// TrustedOrigins widens the CSRF same-origin check (checkOrigin) with
	// origins that are legitimate even though they don't match the request's
	// Host — the reverse-proxy case, where the proxy hands the hub the ingress
	// address instead of the public domain. Full origins
	// ("https://obs.example.com"); scheme+host is compared, case- and
	// trailing-slash-insensitive. Chart-generated (auth.trustedOrigins), plus
	// AVURUOBS_PUBLIC_URL when set.
	TrustedOrigins []string
	// OriginCheck is how a cross-origin write is treated: "" / "enforce"
	// (default) rejects it, "log" lets it through and logs the Origin/Host pair
	// that would have been rejected (how you find out what a proxy actually
	// sends), "off" skips the check entirely. Lowering it disarms a CSRF
	// defense — TrustedOrigins is the narrow fix. Injected as
	// AVURUOBS_AUTH_ORIGIN_CHECK.
	OriginCheck string
	// Demo mode: when DemoEnabled, POST /api/v1/auth/demo signs in as the
	// read-only demo viewer using DemoEmail/DemoPassword server-side (the shared
	// password never reaches the browser), and /auth/config advertises it.
	DemoEnabled  bool
	DemoEmail    string
	DemoPassword string
	// GreenConfig returns the current green (energy/carbon) configuration
	// (hot-reloaded, like GroupsConfig). nil → green.Default(). Unread until
	// the green read path lands; declared now so the cmd/hub wiring is stable.
	GreenConfig func() green.Config
	// OIDC returns the current OIDC provider or nil (hot-reloaded; nil until
	// discovery succeeds / when OIDC unconfigured).
	OIDC func() *auth.OIDCProvider
	// OIDCSettings returns the current parsed OIDC config (nil when unset).
	OIDCSettings func() *auth.OIDCConfig
	// OIDCMapping is the merged (chart config + DB overlay) OIDC group→role
	// mapping cache — non-nil exactly when OIDC is configured (cmd/hub/oidc.go
	// creates it in the same success path that discovers the provider). The
	// admin CRUD at /api/v1/auth/oidc/mapping/* (oidc_mapping.go) reads
	// Snapshot() for its list and calls Refresh after every write, so THIS
	// replica reflects an admin's own edit immediately; nil disables the
	// routes entirely (404) rather than answering from an empty mapping.
	OIDCMapping *auth.MappingCache
	// IngestInternalToken authenticates the gateway→hub ingest-key validation
	// call (auth Plan C). When empty, POST /internal/v1/ingest-keys/validate is
	// not registered and gateway enforcement is simply unused (the drop-in
	// default). Chart-generated, injected as AVURUOBS_INGEST_INTERNAL_TOKEN.
	IngestInternalToken string
	// CollectionRuntimeControlEnabled gates GET/PUT/DELETE
	// /api/v1/collection/overlay and is echoed in GET /api/v1/capabilities
	// (design/2026-07-27-collection-control-plane.md). Chart-generated,
	// injected as AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED.
	CollectionRuntimeControlEnabled bool
	// CollectionApplier pushes an accepted overlay to the cluster. nil
	// defaults to collection.NoopApplier{} in Register.
	CollectionApplier collection.Applier
	// Topology returns the current service-map topology configuration
	// (hot-reloaded, like Groups): which workload names are transport
	// infrastructure rather than applications. nil → topology.Default().
	Topology func() topology.Config
	// StorageConnection describes where telemetry is stored, for the
	// admin-only Settings → Storage view. It carries NO password: the whole
	// point of showing it is "which server and database am I looking at",
	// which the credential does not answer. Zero value → the view omits the
	// connection card.
	StorageConnection StorageConnection
}

// StorageConnection is the non-secret half of the ClickHouse connection.
// Deliberately not editable from the UI: ClickHouse is the store, so it cannot
// hold its own connection string — changing it is a chart value and a restart.
type StorageConnection struct {
	Address  string
	Database string
	Username string
}

// API holds handler dependencies.
type API struct {
	provider          StoreProvider
	cfg               Config
	modules           modules.Set
	tenants           tenantCache
	projects          projectCache
	collectionApplier collection.Applier
	// routes is every registered route with the guard it enforces, captured
	// by routeIndex during Register and read only by the permissions matrix.
	routes []routeGuard
	// collectionMu guards the save→apply pair on the overlay routes so the
	// last write to reach storage is also the last one applied to the
	// cluster. Without it two concurrent admin writes can interleave between
	// the two steps and leave storage saying B while the sensors run A (and
	// the losing apply 502s on a ConfigMap conflict for good measure).
	// Process-local, and enough for the same reason the alerting evaluator's
	// single-loop assumption is: hub replicas default to 1 (HA needs leader
	// election, which is v2).
	collectionMu sync.Mutex
	// lastUsed debounces API-token LastUsedAt writes; see auth.TouchWindow.
	lastUsed auth.LastUsed
}

// store resolves the current backend or fails with 503.
func (a *API) store() (storage.Store, error) {
	if s := a.provider(); s != nil {
		return s, nil
	}
	return nil, errStoreUnavailable
}

// Register mounts the API routes for the active modules on serveMux.
func Register(serveMux *http.ServeMux, provider StoreProvider, cfg Config) {
	active := cfg.Modules
	if active == nil {
		active = modules.AllSet()
	}
	a := &API{provider: provider, cfg: cfg, modules: active}
	if cfg.CollectionApplier != nil {
		a.collectionApplier = cfg.CollectionApplier
	} else {
		a.collectionApplier = collection.NoopApplier{}
	}
	// Everything below registers through the index, which records each route's
	// guard on the way to the real mux. That is what makes the permissions
	// matrix (GET /api/v1/auth/permissions) a reading of the routing table
	// rather than a second copy of it — there is no way to add a route here
	// that the matrix does not see. a.routes is filled at the end, before any
	// request can arrive.
	mux := newRouteIndex(serveMux)
	defer func() { a.routes = mux.routes }()

	// core — never disableable (the wedge: service map + traces + RED).
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /api/v1/status", a.secured(auth.RoleViewer, a.handleStatus))
	mux.Handle("GET /api/v1/capabilities", a.secured(auth.RoleViewer, a.handleCapabilities))
	mux.Handle("GET /api/v1/projects", a.secured(auth.RoleViewer, a.handleProjects))
	mux.Handle("POST /api/v1/projects", a.securedAdmin(a.handleCreateProject))
	mux.Handle("PUT /api/v1/projects/{id}", a.securedAdmin(a.handleUpdateProject))
	mux.Handle("DELETE /api/v1/projects/{id}", a.securedAdmin(a.handleDeleteProject))
	// Per-project ingest keys (auth Plan C) — global admin manages them; the
	// create response is the only time the raw secret is returned.
	mux.Handle("GET /api/v1/projects/{id}/keys", a.securedAdmin(a.handleListIngestKeys))
	mux.Handle("POST /api/v1/projects/{id}/keys", a.securedAdmin(a.handleCreateIngestKey))
	mux.Handle("DELETE /api/v1/projects/{id}/keys/{hash}", a.securedAdmin(a.handleRevokeIngestKey))
	// Gateway-facing ingest-key validation (auth Plan C) — a service-to-service
	// call guarded by the shared internal token, NOT the session middleware.
	// Registered only when the token is configured; empty token → enforcement
	// unused (the safe default).
	if cfg.IngestInternalToken != "" {
		mux.Handle("POST /internal/v1/ingest-keys/validate", handle(a.handleValidateIngestKey))
	}
	// Runtime collection overlay (design/2026-07-27-collection-control-plane.md)
	// — default off; the route set (and its RBAC in the chart) only exists
	// when an install opts in.
	if cfg.CollectionRuntimeControlEnabled {
		mux.Handle("GET /api/v1/collection/overlay", a.securedAdmin(a.handleGetCollectionOverlay))
		mux.Handle("PUT /api/v1/collection/overlay", a.securedAdmin(a.handlePutCollectionOverlay))
		mux.Handle("DELETE /api/v1/collection/overlay", a.securedAdmin(a.handleDeleteCollectionOverlay))
	}
	// system/status is instance-wide (disk capacity, retained-row counts) —
	// not project data, so a single-project viewer or the anonymous demo
	// identity has no business seeing it. Global admin only.
	mux.Handle("GET /api/v1/system/status", a.securedAdmin(a.handleSystemStatus))
	mux.Handle("GET /api/v1/services", a.secured(auth.RoleViewer, a.handleServices))
	// Business tags present in the window, for filter discovery. Core: tags
	// ride resource attributes on traces, which every install collects.
	mux.Handle("GET /api/v1/tags", a.secured(auth.RoleViewer, a.handleTags))
	mux.Handle("GET /api/v1/service-map", a.secured(auth.RoleViewer, a.handleServiceMap))
	mux.Handle("GET /api/v1/traces", a.secured(auth.RoleViewer, a.handleSearchTraces))
	mux.Handle("GET /api/v1/traces/overview", a.secured(auth.RoleViewer, a.handleTraceOverview))
	mux.Handle("GET /api/v1/traces/heatmap", a.secured(auth.RoleViewer, a.handleHeatmap))
	mux.Handle("GET /api/v1/traces/{traceId}", a.secured(auth.RoleViewer, a.handleGetTrace))
	mux.Handle("GET /api/v1/spans/{spanId}", a.secured(auth.RoleViewer, a.handleGetSpan))
	mux.Handle("GET /api/v1/metrics/red", a.secured(auth.RoleViewer, a.handleREDSeries))

	// /auth/config is always registered, auth on or off — the SPA's login
	// page needs a straight answer, not a 404 it has to special-case.
	mux.Handle("GET /api/v1/auth/config", handle(a.handleAuthConfig))
	// The permissions matrix. Readable by any signed-in user (and by anyone on
	// an auth-off install, where it reports exactly that): knowing which role
	// may do what is how you understand a refusal, not a privilege. Registered
	// unconditionally so the Settings screen behaves the same either way.
	mux.Handle("GET /api/v1/auth/permissions", a.authenticated(a.handlePermissions))
	if cfg.Auth != nil {
		mux.Handle("POST /api/v1/auth/login", handle(a.handleLogin))
		mux.Handle("POST /api/v1/auth/logout", a.authenticated(a.handleLogout))
		mux.Handle("GET /api/v1/auth/me", a.authenticated(a.handleMe))
		// Self-service password change — authenticated(), not securedAdmin:
		// it rotates the CALLER's own credential, so a zero-grant user must
		// reach it too.
		mux.Handle("POST /api/v1/auth/password", a.authenticated(a.handleChangePassword))

		// Personal API tokens (design/2026-08-13-api-tokens.md) —
		// authenticated(), not secured(): a user whose grants were all
		// revoked must still be able to clean up the credentials they handed
		// out, for the same reason they can still log out. GET widens to
		// another user's tokens only for a global admin, checked INSIDE the
		// handler via id.IsAdmin() — one URL, one resource, not a second
		// admin-only route.
		mux.Handle("GET /api/v1/tokens", a.authenticated(a.handleListAPITokens))
		mux.Handle("POST /api/v1/tokens", a.authenticated(a.handleCreateAPIToken))
		mux.Handle("DELETE /api/v1/tokens/{hash}", a.authenticated(a.handleRevokeAPIToken))

		// Demo one-click login — registered only when demo mode is on. Signs in
		// as the read-only demo viewer server-side (shared password stays server
		// -held); establishes the session, so it is unauthenticated like /login.
		if cfg.DemoEnabled {
			mux.Handle("POST /api/v1/auth/demo", handle(a.handleDemoLogin))
		}

		// OIDC SSO — unauthenticated (they establish the session): start the
		// auth-code flow and handle the IdP redirect back. Registered even when
		// no provider is wired yet (nil → 400 "OIDC is not configured"), so the
		// route set is stable and the SPA gets a straight answer.
		mux.Handle("GET /api/v1/auth/oidc/start", handle(a.handleOIDCStart))
		mux.Handle("GET /api/v1/auth/oidc/callback", handle(a.handleOIDCCallback))

		// OIDC group→role mapping overlay (oidc_mapping.go) — registered only
		// when OIDC is actually configured (cfg.OIDCMapping != nil), unlike
		// start/callback above which stay registered and answer 400 so the
		// route set is stable: there is no mapping to edit at all when SSO
		// isn't wired, so 404 is the honest answer here, not a redundant "not
		// configured" body. Admin-gated on every verb, including the read —
		// this configures WHO gets WHAT role, which is more sensitive than the
		// service-health groupings a viewer may read.
		if cfg.OIDCMapping != nil {
			mux.Handle("GET /api/v1/auth/oidc/mapping", a.securedAdmin(a.handleOIDCMapping))
			mux.Handle("PUT /api/v1/auth/oidc/mapping/{group}", a.securedAdmin(a.handleUpsertOIDCMapping))
			mux.Handle("DELETE /api/v1/auth/oidc/mapping/{group}", a.securedAdmin(a.handleDeleteOIDCMapping))
			mux.Handle("POST /api/v1/auth/oidc/mapping/reset", a.securedAdmin(a.handleResetOIDCMapping))
		}

		// Users admin API — global admin only (creates/edits credentials and
		// grants for OTHER users, unlike /auth/me which is self-service).
		mux.Handle("GET /api/v1/users", a.securedAdmin(a.handleListUsers))
		mux.Handle("POST /api/v1/users", a.securedAdmin(a.handleCreateUser))
		mux.Handle("PUT /api/v1/users/{id}", a.securedAdmin(a.handleUpdateUser))
		mux.Handle("DELETE /api/v1/users/{id}", a.securedAdmin(a.handleDeleteUser))
	}

	if active.Enabled(modules.Logs) {
		mux.Handle("GET /api/v1/logs", a.secured(auth.RoleViewer, a.handleSearchLogs))
		mux.Handle("GET /api/v1/traces/{traceId}/logs", a.secured(auth.RoleViewer, a.handleLogsForTrace))
	}
	if active.Enabled(modules.Profiling) {
		mux.Handle("GET /api/v1/profiles/services", a.secured(auth.RoleViewer, a.handleProfiledServices))
		mux.Handle("GET /api/v1/profiles/flamegraph", a.secured(auth.RoleViewer, a.handleFlamegraph))
		// Machine ingest — session auth does not apply; ingest keys (Plan C) will.
		// OTLP profiles ingest (alpha signal) — deliberately NOT under
		// /api/v1; this is the otlphttp exporter's default profiles path.
		mux.Handle("POST /v1development/profiles", handle(a.handleProfilesIngest))
	}
	if active.Enabled(modules.InfraMetrics) {
		mux.Handle("GET /api/v1/infra/nodes", a.secured(auth.RoleViewer, a.handleInfraNodes))
		mux.Handle("GET /api/v1/infra/pods", a.secured(auth.RoleViewer, a.handleInfraPods))
		// The sensor inventory reads collector self-metrics from the metrics
		// tables, so it lives with infra-metrics (see the module AEP).
		mux.Handle("GET /api/v1/agents", a.secured(auth.RoleViewer, a.handleAgents))
		// Cross-zone byte volume — sensor counters in the same metrics tables,
		// hence the same module gate. Not a service-map extension: zones are
		// node topology, not graph elements.
		mux.Handle("GET /api/v1/network/zones", a.secured(auth.RoleViewer, a.handleZoneTraffic))
	}
	if active.Enabled(modules.ErrorTracking) {
		mux.Handle("GET /api/v1/errors/issues", a.secured(auth.RoleViewer, a.handleSearchErrorIssues))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}", a.secured(auth.RoleViewer, a.handleGetErrorIssue))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}/events", a.secured(auth.RoleViewer, a.handleListErrorEvents))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}/histogram", a.secured(auth.RoleViewer, a.handleErrorIssueHistogram))
		mux.Handle("POST /api/v1/errors/issues/{fingerprint}/status", a.secured(auth.RoleEditor, a.handleSetErrorIssueStatus))
	}
	if active.Enabled(modules.ServiceHealth) {
		mux.Handle("GET /api/v1/health/groups", a.secured(auth.RoleViewer, a.handleHealthGroups))
		mux.Handle("GET /api/v1/health/groups/{name}", a.secured(auth.RoleViewer, a.handleHealthGroup))
		// Group definitions (what the groups ARE), as opposed to /health/groups
		// (how they are DOING). Reads follow the health authorization; writes
		// are admin-only, like every other configuration surface.
		mux.Handle("GET /api/v1/service-groups", a.secured(auth.RoleViewer, a.handleServiceGroups))
		mux.Handle("POST /api/v1/service-groups", a.securedAdmin(a.handleCreateServiceGroup))
		mux.Handle("PUT /api/v1/service-groups/{name}", a.securedAdmin(a.handleUpdateServiceGroup))
		mux.Handle("DELETE /api/v1/service-groups/{name}", a.securedAdmin(a.handleDeleteServiceGroup))
	}
	if active.Enabled(modules.Alerting) {
		mux.Handle("GET /api/v1/alerts", a.secured(auth.RoleViewer, a.handleAlerts))
		mux.Handle("GET /api/v1/alerts/rules", a.secured(auth.RoleViewer, a.handleAlertRules))
		mux.Handle("GET /api/v1/alerts/channels", a.secured(auth.RoleViewer, a.handleListAlertChannels))
		mux.Handle("POST /api/v1/alerts/channels", a.securedAdmin(a.handleCreateAlertChannel))
		mux.Handle("PUT /api/v1/alerts/channels/{name}", a.securedAdmin(a.handleUpdateAlertChannel))
		mux.Handle("DELETE /api/v1/alerts/channels/{name}", a.securedAdmin(a.handleDeleteAlertChannel))
		mux.Handle("POST /api/v1/alerts/channels/{name}/test", a.securedAdmin(a.handleTestAlertChannel))
	}
	if active.Enabled(modules.Mesh) {
		// The proxies read core trace data; the control plane reads the metrics
		// tables and says so itself when infra-metrics is off. One module gate,
		// so the screen either exists whole or not at all.
		mux.Handle("GET /api/v1/mesh/proxies", a.secured(auth.RoleViewer, a.handleMeshProxies))
		mux.Handle("GET /api/v1/mesh/control-plane", a.secured(auth.RoleViewer, a.handleMeshControlPlane))
	}
	if active.Enabled(modules.Green) {
		mux.Handle("GET /api/v1/green/summary", a.secured(auth.RoleViewer, a.handleGreenSummary))
		mux.Handle("GET /api/v1/green/budgets", a.secured(auth.RoleViewer, a.handleGreenBudgets))
		mux.Handle("GET /api/v1/green/report", a.secured(auth.RoleViewer, a.handleGreenReport))
	}
}

// groupsConfig resolves the active service-health config — chart-declared and
// UI-authored groups merged — defaulting to Default() (auto-only grouping)
// when no resolver is wired. Every health computation goes through here; the
// nil case is handled by the resolver itself.
func (a *API) groupsConfig(ctx context.Context) health.Config {
	return a.cfg.Groups.Config(ctx)
}

// topologyClassifier builds the transport classifier from the active topology
// config, defaulting to Default() (the built-in mesh patterns) when none is
// wired (mirrors greenConfig/alertsConfig). Built once per request and shared
// across that response's services and edges, so one map cannot disagree with
// itself mid-render.
func (a *API) topologyClassifier() topology.Classifier {
	if a.cfg.Topology != nil {
		return topology.New(a.cfg.Topology())
	}
	return topology.New(topology.Default())
}

// alertsConfig resolves the active alerting config (for the read-only /rules
// endpoint), defaulting to Default() when none is wired.
func (a *API) alertsConfig() alerting.Config {
	if a.cfg.AlertsConfig != nil {
		return a.cfg.AlertsConfig()
	}
	return alerting.Default()
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type statusResponse struct {
	Service    string `json:"service"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	ClickHouse string `json:"clickhouse"`
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) error {
	resp := statusResponse{Service: "avuru-hub", Version: Version, Status: "ok", ClickHouse: "unreachable"}
	if s := a.provider(); s != nil {
		if err := s.Ping(r.Context()); err == nil {
			resp.ClickHouse = "ok"
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding response", "error", err)
	}
}
