package api

import (
	"context"
	"log/slog"
	"net/http"

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
	Name string `json:"name"`
	// Namespace and Role are omitted rather than defaulted when unknown. A
	// proxy filed under "default" or under a guessed role is worse than one
	// filed under neither, because a guess in a table is read as a fact.
	Namespace string `json:"namespace,omitempty"`
	// Role is the topology.MeshRole: which KIND of proxy this is. A ztunnel
	// carrying everything its node sends and a waypoint that only sees traffic
	// routed to it fail differently and are read differently.
	Role       string  `json:"role,omitempty"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	CallsIn    uint64  `json:"callsIn"`
	CallsOut   uint64  `json:"callsOut"`
	// Bytes and connection health are what OBI measured on the wire, and they
	// are POINTERS because their absence is a fact about the install, not a
	// zero. A proxy reported as carrying 0 bytes is indistinguishable from one
	// that has failed; an install without the infra-metrics module must get no
	// field at all and render a gap.
	BytesIn           *uint64  `json:"bytesIn,omitempty"`
	BytesOut          *uint64  `json:"bytesOut,omitempty"`
	RTTMs             *float64 `json:"rttMs,omitempty"`
	FailedConnections *uint64  `json:"failedConnections,omitempty"`
	Retransmits       *uint64  `json:"retransmits,omitempty"`
	// What the proxy's own counters said about traffic addressed TO it, from
	// the data-plane scrape. Pointers for the same reason as the bytes: a
	// proxy the scrape did not report is a gap, not a fully encrypted one.
	MTLSShare      *float64 `json:"mtlsShare,omitempty"`
	PlaintextUnits *uint64  `json:"plaintextUnits,omitempty"`
	// ztunnel rows only: what the node proxies are carrying, how much they
	// have been told about and not yet wired, and how often their control-
	// plane stream was cut. These are FLEET totals stamped on every ztunnel
	// row — the gauges are summed across pods at their latest value, and the
	// trace-derived rows are per service name, which for a DaemonSet is one
	// row for the whole fleet anyway.
	ActiveWorkloads  *uint64 `json:"activeWorkloads,omitempty"`
	PendingWorkloads *uint64 `json:"pendingWorkloads,omitempty"`
	XDSTerminations  *uint64 `json:"xdsTerminations,omitempty"`
}

type meshProxiesResponse struct {
	Proxies []meshProxyDTO `json:"proxies"`
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

	// Best-effort, exactly as the map treats it: a namespace is how you find a
	// proxy among forty, not what tells you it is broken. Losing the lookup
	// costs a facet; failing the request costs the screen.
	var namespaces map[string]string
	if labels, lerr := store.ServiceLabels(r.Context(), q); lerr == nil {
		namespaces = serviceNamespaces(labels)
	}

	// The wire view, when this install collects one. Same gate the map uses:
	// otel_metrics_* exists only with infra-metrics, so querying it otherwise
	// errors rather than returning nothing.
	var flows []storage.ServiceEdge
	var health []storage.NetworkEdgeHealth
	if a.modules.Enabled(modules.InfraMetrics) {
		if flows, err = store.NetworkEdges(r.Context(), q); err != nil {
			return err
		}
		if health, err = store.NetworkEdgeHealth(r.Context(), q); err != nil {
			return err
		}
	}
	measured := meshFlows(cls, flows, health)
	// The data plane's own account: optional reads, gated like the flows, and
	// a failure costs the fields rather than the screen (the ServiceLabels
	// pattern) — the RED is still worth showing without them.
	var secured map[nsWorkloadKey]*observed
	var ztunnel *storage.MeshZtunnelHealth
	if a.modules.Enabled(modules.InfraMetrics) {
		q.MeshDataplaneJob = a.cfg.MeshDataplaneJob
		secured, ztunnel = a.meshProxySecurity(r.Context(), store, q)
	}

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
		row := meshProxyDTO{
			Name:      d.Name,
			Namespace: namespaces[s.Name],
			// The labels ride on ServiceStats, so the role is decided from the
			// same rows the RED came from — one read, one set of workloads.
			Role:       string(cls.MeshRole(s.Name, s.TransportLabels)),
			RatePerSec: d.RatePerSec,
			ErrorRate:  d.ErrorRate,
			P50Ms:      d.P50Ms,
			P95Ms:      d.P95Ms,
			CallsIn:    in[s.Name],
			CallsOut:   out[s.Name],
		}
		if f := measured[s.Name]; f != nil {
			if f.measuredBytes {
				row.BytesIn, row.BytesOut = &f.bytesIn, &f.bytesOut
			}
			if f.measuredHealth {
				row.RTTMs = &f.rttMs
				row.FailedConnections = &f.failedConnections
				row.Retransmits = &f.retransmits
			}
		}
		if o := secured[nsWorkloadKeyOf(s.Name, namespaces)]; o != nil {
			row.MTLSShare = o.mtlsShare()
			plaintext := o.plaintext
			row.PlaintextUnits = &plaintext
		}
		if ztunnel != nil && row.Role == string(topology.MeshRoleZtunnel) {
			active, pending, cut := ztunnel.ActiveWorkloads, ztunnel.PendingWorkloads, ztunnel.XDSConnectionTerminations
			row.ActiveWorkloads, row.PendingWorkloads, row.XDSTerminations = &active, &pending, &cut
		}
		resp.Proxies = append(resp.Proxies, row)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// meshProxySecurity reads what the data plane said about the proxies
// themselves: per-workload security keyed the mesh's way, and the ztunnel
// fleet's own counts. Either read failing is logged and yields nil — the
// fields it feeds stay absent, which is the truth about them.
func (a *API) meshProxySecurity(
	ctx context.Context, store storage.Store, q storage.ServiceQuery,
) (map[nsWorkloadKey]*observed, *storage.MeshZtunnelHealth) {
	var secured map[nsWorkloadKey]*observed
	if sec, err := store.MeshSecurity(ctx, q); err != nil {
		slog.Warn("mesh proxies: data-plane security read failed; rows carry no mTLS share", "error", err)
	} else if sec.Available {
		secured = make(map[nsWorkloadKey]*observed, len(sec.Workloads))
		for _, w := range sec.Workloads {
			secured[nsWorkloadKey{w.Namespace, w.Workload}] = observe(w)
		}
	}
	var ztunnel *storage.MeshZtunnelHealth
	if zt, err := store.MeshZtunnelHealth(ctx, q); err != nil {
		slog.Warn("mesh proxies: ztunnel read failed; rows carry no workload counts", "error", err)
	} else if zt.Measured {
		ztunnel = &zt
	}
	return secured, ztunnel
}

// nsWorkloadKeyOf keys a traced service the mesh's way, or under an empty
// namespace when none is known — which matches nothing, on purpose.
func nsWorkloadKeyOf(service string, namespaces map[string]string) nsWorkloadKey {
	ns, wl := workloadKey(service, namespaces[service])
	return nsWorkloadKey{ns, wl}
}
