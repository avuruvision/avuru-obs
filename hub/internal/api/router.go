// Package api owns the hub's HTTP surface: route registration and handlers.
// Business logic belongs in the service/storage layers, not here.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
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
	mux.Handle("GET /api/v1/status", handle(a.handleStatus))
	mux.Handle("GET /api/v1/capabilities", handle(a.handleCapabilities))
	mux.Handle("GET /api/v1/projects", handle(a.handleProjects))
	mux.Handle("GET /api/v1/system/status", handle(a.handleSystemStatus))
	mux.Handle("GET /api/v1/services", handle(a.handleServices))
	mux.Handle("GET /api/v1/service-map", handle(a.handleServiceMap))
	mux.Handle("GET /api/v1/traces", handle(a.handleSearchTraces))
	mux.Handle("GET /api/v1/traces/overview", handle(a.handleTraceOverview))
	mux.Handle("GET /api/v1/traces/heatmap", handle(a.handleHeatmap))
	mux.Handle("GET /api/v1/traces/{traceId}", handle(a.handleGetTrace))
	mux.Handle("GET /api/v1/spans/{spanId}", handle(a.handleGetSpan))
	mux.Handle("GET /api/v1/metrics/red", handle(a.handleREDSeries))

	if active.Enabled(modules.Logs) {
		mux.Handle("GET /api/v1/logs", handle(a.handleSearchLogs))
		mux.Handle("GET /api/v1/traces/{traceId}/logs", handle(a.handleLogsForTrace))
	}
	if active.Enabled(modules.Profiling) {
		mux.Handle("GET /api/v1/profiles/services", handle(a.handleProfiledServices))
		mux.Handle("GET /api/v1/profiles/flamegraph", handle(a.handleFlamegraph))
		// OTLP profiles ingest (alpha signal) — deliberately NOT under
		// /api/v1; this is the otlphttp exporter's default profiles path.
		mux.Handle("POST /v1development/profiles", handle(a.handleProfilesIngest))
	}
	if active.Enabled(modules.InfraMetrics) {
		mux.Handle("GET /api/v1/infra/nodes", handle(a.handleInfraNodes))
		mux.Handle("GET /api/v1/infra/pods", handle(a.handleInfraPods))
		// The sensor inventory reads collector self-metrics from the metrics
		// tables, so it lives with infra-metrics (see the module AEP).
		mux.Handle("GET /api/v1/agents", handle(a.handleAgents))
	}
	if active.Enabled(modules.ErrorTracking) {
		mux.Handle("GET /api/v1/errors/issues", handle(a.handleSearchErrorIssues))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}", handle(a.handleGetErrorIssue))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}/events", handle(a.handleListErrorEvents))
		mux.Handle("GET /api/v1/errors/issues/{fingerprint}/histogram", handle(a.handleErrorIssueHistogram))
		mux.Handle("POST /api/v1/errors/issues/{fingerprint}/status", handle(a.handleSetErrorIssueStatus))
	}
	if active.Enabled(modules.ServiceHealth) {
		mux.Handle("GET /api/v1/health/groups", handle(a.handleHealthGroups))
		mux.Handle("GET /api/v1/health/groups/{name}", handle(a.handleHealthGroup))
	}
	if active.Enabled(modules.Alerting) {
		mux.Handle("GET /api/v1/alerts", handle(a.handleAlerts))
		mux.Handle("GET /api/v1/alerts/rules", handle(a.handleAlertRules))
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
