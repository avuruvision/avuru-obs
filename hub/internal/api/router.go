// Package api owns the hub's HTTP surface: route registration and handlers.
// Business logic belongs in the service/storage layers, not here.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/green"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
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
	// Projects declared through deployment config (AVURUOPS_PROJECTS) —
	// merged with data-observed tenants by GET /api/v1/projects.
	Projects []string
	// Modules is the active-module set (AVURUOPS_MODULES); nil means all
	// modules — the backward-compatible default. Routes owned by an inactive
	// module are not registered (404).
	Modules modules.Set
	// GroupsConfig returns the current service-health configuration. It is a
	// function so the value can be hot-reloaded (the loader swaps an
	// atomic.Pointer) without a hub restart. nil → health.Default().
	GroupsConfig func() health.Config
	// AlertsConfig returns the current alerting configuration (hot-reloaded).
	// Read-only endpoint use only; the evaluator lives in cmd/hub. nil →
	// alerting.Default().
	AlertsConfig func() alerting.Config
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
}

// API holds handler dependencies.
type API struct {
	provider StoreProvider
	cfg      Config
	modules  modules.Set
	tenants  tenantCache
}

// store resolves the current backend or fails with 503.
func (a *API) store() (storage.Store, error) {
	if s := a.provider(); s != nil {
		return s, nil
	}
	return nil, errStoreUnavailable
}

// Register mounts the API routes for the active modules on mux.
func Register(mux *http.ServeMux, provider StoreProvider, cfg Config) {
	active := cfg.Modules
	if active == nil {
		active = modules.AllSet()
	}
	a := &API{provider: provider, cfg: cfg, modules: active}

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
	// system/status is instance-wide (disk capacity, retained-row counts) —
	// not project data, so a single-project viewer or the anonymous demo
	// identity has no business seeing it. Global admin only.
	mux.Handle("GET /api/v1/system/status", a.securedAdmin(a.handleSystemStatus))
	mux.Handle("GET /api/v1/services", a.secured(auth.RoleViewer, a.handleServices))
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
	if cfg.Auth != nil {
		mux.Handle("POST /api/v1/auth/login", handle(a.handleLogin))
		mux.Handle("POST /api/v1/auth/logout", a.authenticated(a.handleLogout))
		mux.Handle("GET /api/v1/auth/me", a.authenticated(a.handleMe))

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

		// Users admin API — global admin only (creates/edits credentials and
		// grants for OTHER users, unlike /auth/me which is self-service).
		mux.Handle("GET /api/v1/users", a.securedAdmin(a.handleListUsers))
		mux.Handle("POST /api/v1/users", a.securedAdmin(a.handleCreateUser))
		mux.Handle("PUT /api/v1/users/{id}", a.securedAdmin(a.handleUpdateUser))
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
	if active.Enabled(modules.Green) {
		mux.Handle("GET /api/v1/green/summary", handle(a.handleGreenSummary))
		mux.Handle("GET /api/v1/green/budgets", handle(a.handleGreenBudgets))
		mux.Handle("GET /api/v1/green/report", handle(a.handleGreenReport))
	}
}

// groupsConfig resolves the active service-health config, defaulting to
// Default() (auto-only grouping) when none is wired.
func (a *API) groupsConfig() health.Config {
	if a.cfg.GroupsConfig != nil {
		return a.cfg.GroupsConfig()
	}
	return health.Default()
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
