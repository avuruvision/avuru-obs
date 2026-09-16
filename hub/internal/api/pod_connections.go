package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type podRefDTO struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Node      string `json:"node"`
	Service   string `json:"service"`
}

type podConnectionDTO struct {
	Source podRefDTO `json:"source"`
	Target podRefDTO `json:"target"`
	Peer   string    `json:"peer,omitempty"`
	Calls  uint64    `json:"calls"`
	Errors uint64    `json:"errors"`
	P95Ms  float64   `json:"p95Ms"`
}

type podConnectionsResponse struct {
	Connections []podConnectionDTO `json:"connections"`
	Truncated   bool               `json:"truncated"`
	Limit       int                `json:"limit"`
}

func (a *API) handlePodConnections(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 200)
	if err != nil {
		return err
	}
	if limit < 1 || limit > 500 {
		return badRequest("limit must be between 1 and 500")
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	edges, err := store.PodConnections(r.Context(), storage.InfraQuery{
		Tenant: tenant, Tenants: tenants, Range: tr, Node: r.URL.Query().Get("node"), Limit: limit + 1,
	})
	if err != nil {
		return err
	}
	resp := podConnectionsResponse{Connections: make([]podConnectionDTO, 0, min(len(edges), limit)), Limit: limit, Truncated: len(edges) > limit}
	if resp.Truncated {
		edges = edges[:limit]
	}
	ref := func(p storage.PodRef) podRefDTO { return podRefDTO{p.Name, p.Namespace, p.Node, p.Service} }
	for _, edge := range edges {
		resp.Connections = append(resp.Connections, podConnectionDTO{
			Source: ref(edge.Source), Target: ref(edge.Target), Peer: edge.Peer,
			Calls: edge.Calls, Errors: edge.Errors, P95Ms: float64(edge.P95) / float64(time.Millisecond),
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
