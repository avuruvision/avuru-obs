package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

// meshProxyDTO is one transport workload's own RED, plus the load it carries.
//
// CallsIn/CallsOut are what makes it a PROXY view rather than another service
// row: a sidecar with traffic arriving and none leaving is failing to forward,
// which its own error rate may not show at all.
type meshProxyDTO struct {
	Name       string  `json:"name"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	CallsIn    uint64  `json:"callsIn"`
	CallsOut   uint64  `json:"callsOut"`
}

type meshProxiesResponse struct {
	Proxies []meshProxyDTO `json:"proxies"`
}

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
}

// handleMeshProxies lists the mesh's own workloads with their RED and the call
// volume they carry.
//
// No new SQL: these are services already in the tables, and the only reason
// they are absent from every other screen is the view decision the service map
// makes. Reading them is the same ListServices call with the classifier's
// verdict inverted — if this needed a query of its own, that would be a sign
// the classification had ended up in the wrong place.
func (a *API) handleMeshProxies(w http.ResponseWriter, r *http.Request) error {
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
	q := storage.ServiceQuery{
		Tenant: tenant, Tenants: tenants, Range: tr,
		// Aux traffic stays excluded, as everywhere else: a proxy's health
		// checks are not the traffic anyone is asking about.
		ExcludeAux: !parseBool(r, "includeAux", false),
	}
	services, err := store.ListServices(r.Context(), q)
	if err != nil {
		return err
	}
	edges, err := store.ServiceEdges(r.Context(), q)
	if err != nil {
		return err
	}

	cls := a.topologyClassifier().WithEvidence(topology.LabelledTransport(services))
	in := map[string]uint64{}
	out := map[string]uint64{}
	for _, e := range edges {
		if cls.IsTransport(e.Target) {
			in[e.Target] += e.Count
		}
		if cls.IsTransport(e.Source) {
			out[e.Source] += e.Count
		}
	}

	window := tr.End.Sub(tr.Start)
	resp := meshProxiesResponse{Proxies: []meshProxyDTO{}}
	for _, s := range services {
		if !cls.IsTransport(s.Name) {
			continue
		}
		d := toServiceDTO(s, window)
		resp.Proxies = append(resp.Proxies, meshProxyDTO{
			Name:       d.Name,
			RatePerSec: d.RatePerSec,
			ErrorRate:  d.ErrorRate,
			P50Ms:      d.P50Ms,
			P95Ms:      d.P95Ms,
			CallsIn:    in[s.Name],
			CallsOut:   out[s.Name],
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
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
		Available:        true,
		State:            string(cp.State),
		Kind:             cp.Kind,
		LastSeen:         &seen,
		ConnectedProxies: cp.ConnectedProxies,
		Pushes:           cp.Pushes,
		RejectedConfigs:  cp.RejectedConfigs,
		ConvergenceP95Ms: cp.ConvergenceP95Ms,
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
