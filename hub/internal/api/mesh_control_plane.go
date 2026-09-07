package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// meshControlPlaneResponse deliberately leads with `available`. Every number
// after it is meaningless when it is false, and a client that renders the
// numbers anyway would be reporting a perfectly healthy control plane that
// nobody is watching.
type meshControlPlaneResponse struct {
	Available bool `json:"available"`
	// State says WHY, which `available: false` never could: nothing is
	// scraping, the target is not answering, or it answered with metrics this
	// product cannot read. Three problems, three different fixes
	// (design/2026-08-26-control-plane-diagnosis.md).
	State string `json:"state"`
	// Kind is the control plane whose metrics were recognised ("istio").
	// Empty otherwise — including when something answered and was not
	// understood, which is the case this field exists to make legible.
	Kind string `json:"kind,omitempty"`
	// Reason explains an unavailable control plane in the terms the operator
	// can act on. Empty when available.
	Reason           string     `json:"reason,omitempty"`
	LastSeen         *time.Time `json:"lastSeen,omitempty"`
	ConnectedProxies uint64     `json:"connectedProxies,omitempty"`
	Pushes           uint64     `json:"pushes,omitempty"`
	RejectedConfigs  uint64     `json:"rejectedConfigs,omitempty"`
	ConvergenceP95Ms float64    `json:"convergenceP95Ms,omitempty"`
	// Pointers, because these come from a widened scrape keep-list and an
	// install on an older chart publishes none of them while being perfectly
	// healthy. For WriteTimeouts especially, nil and 0 are opposite answers:
	// "we are not looking" versus "no proxy missed its config".
	PushP95Ms     *float64 `json:"pushP95Ms,omitempty"`
	WriteTimeouts *uint64  `json:"writeTimeouts,omitempty"`
	ConfigEvents  *uint64  `json:"configEvents,omitempty"`
	// ListenerConflicts is configuration the control plane could not program
	// because two pieces of it claim the same listener — served, and wrong.
	// QueueP95Ms is how long a push waited in istiod's queue before being
	// sent: the third place a slow push can be, after the send and the ack.
	// Optional for the same reason as the trio above.
	ListenerConflicts *uint64  `json:"listenerConflicts,omitempty"`
	QueueP95Ms        *float64 `json:"queueP95Ms,omitempty"`
}

// handleMeshControlPlane answers "is the control plane still programming the
// mesh?" — and says so plainly when it cannot.
func (a *API) handleMeshControlPlane(w http.ResponseWriter, r *http.Request) error {
	// The scrape lands in the metrics tables, which exist only with the
	// infra-metrics module. Answering 200 with `available: false` rather than
	// 404: the question is legitimate on this install, we simply have no data
	// for it, and the reason is the actionable part.
	if !a.modules.Enabled(modules.InfraMetrics) {
		writeJSON(w, http.StatusOK, meshControlPlaneResponse{
			State:  string(storage.MeshControlPlaneUnconfigured),
			Reason: "control-plane metrics are stored by the infra-metrics module, which is not enabled on this install",
		})
		return nil
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	cp, err := store.MeshControlPlane(r.Context(), storage.ServiceQuery{
		Tenant: tenant, Tenants: tenants, Range: tr,
		MeshScrapeJob: a.cfg.MeshScrapeJob,
	})
	if err != nil {
		return err
	}
	if !cp.Available {
		writeJSON(w, http.StatusOK, meshControlPlaneResponse{
			State:  string(cp.State),
			Reason: meshUnavailableReason(cp.State),
		})
		return nil
	}
	seen := cp.LastSeen
	writeJSON(w, http.StatusOK, meshControlPlaneResponse{
		Available:         true,
		State:             string(cp.State),
		Kind:              cp.Kind,
		LastSeen:          &seen,
		ConnectedProxies:  cp.ConnectedProxies,
		Pushes:            cp.Pushes,
		RejectedConfigs:   cp.RejectedConfigs,
		ConvergenceP95Ms:  cp.ConvergenceP95Ms,
		PushP95Ms:         cp.PushP95Ms,
		WriteTimeouts:     cp.WriteTimeouts,
		ConfigEvents:      cp.ConfigEvents,
		ListenerConflicts: cp.ListenerConflicts,
		QueueP95Ms:        cp.QueueP95Ms,
	})
	return nil
}

// meshUnavailableReason turns a silence into an instruction. Each of the three
// states has a different fix, and before this they all rendered the same
// sentence — which sent an operator to check a scrape that was working fine.
func meshUnavailableReason(state storage.MeshControlPlaneState) string {
	switch state {
	case storage.MeshControlPlaneUnreachable:
		return "the control-plane scrape is running and the target is not answering — check mesh.controlPlane.endpoint, or the control plane itself is down"
	case storage.MeshControlPlaneUnrecognised:
		return "the scrape target answered, and none of the metrics this product reads came back. The control-plane view is Istio-shaped (pilot_*): a different control plane will show its proxies on this screen but not its own health"
	default:
		return "no control-plane metrics in this window — set mesh.controlPlane.enabled and point it at your control plane"
	}
}
