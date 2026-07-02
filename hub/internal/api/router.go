// Package api owns the hub's HTTP surface: route registration and handlers.
// Business logic belongs in the service/storage layers, not here.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

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
	RetentionTracesDays  int
	RetentionLogsDays    int
	RetentionMetricsDays int
}

// API holds handler dependencies.
type API struct {
	provider StoreProvider
	cfg      Config
}

// store resolves the current backend or fails with 503.
func (a *API) store() (storage.Store, error) {
	if s := a.provider(); s != nil {
		return s, nil
	}
	return nil, errStoreUnavailable
}

// Register mounts all API routes on mux.
func Register(mux *http.ServeMux, provider StoreProvider, cfg Config) {
	a := &API{provider: provider, cfg: cfg}

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /api/v1/status", handle(a.handleStatus))
	mux.Handle("GET /api/v1/system/status", handle(a.handleSystemStatus))
	mux.Handle("GET /api/v1/services", handle(a.handleServices))
	mux.Handle("GET /api/v1/service-map", handle(a.handleServiceMap))
	mux.Handle("GET /api/v1/traces", handle(a.handleSearchTraces))
	mux.Handle("GET /api/v1/traces/overview", handle(a.handleTraceOverview))
	mux.Handle("GET /api/v1/traces/heatmap", handle(a.handleHeatmap))
	mux.Handle("GET /api/v1/traces/{traceId}", handle(a.handleGetTrace))
	mux.Handle("GET /api/v1/traces/{traceId}/logs", handle(a.handleLogsForTrace))
	mux.Handle("GET /api/v1/logs", handle(a.handleSearchLogs))
	mux.Handle("GET /api/v1/infra/nodes", handle(a.handleInfraNodes))
	mux.Handle("GET /api/v1/infra/pods", handle(a.handleInfraPods))
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
