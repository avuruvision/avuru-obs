package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type agentSignalsDTO struct {
	Traces   *string `json:"traces"`
	Logs     *string `json:"logs"`
	Metrics  *string `json:"metrics"`
	Profiles *string `json:"profiles"`
}

type agentNodeDTO struct {
	Node     string          `json:"node"`
	LastSeen string          `json:"lastSeen"`
	Signals  agentSignalsDTO `json:"signals"`
}

type agentsResponse struct {
	// Sensors is per-node: one sensor DaemonSet pod per node.
	Sensors       []agentNodeDTO `json:"sensors"`
	WindowSeconds int            `json:"windowSeconds"`
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// handleAgents serves the sensor inventory, derived from telemetry freshness
// per node (Coroot-style "N nodes reporting"). Read-only: runtime toggles
// arrive with the OpAMP control plane (v0.2).
func (a *API) handleAgents(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	windowSec, err := parseInt(r, "windowSec", 600)
	if err != nil {
		return err
	}
	if windowSec <= 0 {
		return badRequest("windowSec must be positive")
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	nodes, err := store.ListAgentNodes(r.Context(), storage.AgentQuery{
		Tenant:  tenant,
		Tenants: tenants,
		Window:  time.Duration(windowSec) * time.Second,
	})
	if err != nil {
		return err
	}
	resp := agentsResponse{Sensors: make([]agentNodeDTO, 0, len(nodes)), WindowSeconds: windowSec}
	for _, n := range nodes {
		resp.Sensors = append(resp.Sensors, agentNodeDTO{
			Node:     n.Node,
			LastSeen: n.LastSeen.UTC().Format(time.RFC3339),
			Signals: agentSignalsDTO{
				Traces:   rfc3339Ptr(n.Traces),
				Logs:     rfc3339Ptr(n.Logs),
				Metrics:  rfc3339Ptr(n.Metrics),
				Profiles: rfc3339Ptr(n.Profiles),
			},
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
