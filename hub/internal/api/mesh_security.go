package api

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

// meshObservedDTO is what one proxy reported about one workload's traffic.
// Units are requests plus connections; the two totals say how the units split,
// and MTLSShare is nil when nothing was classified — 0/0 is not 0 %.
type meshObservedDTO struct {
	Reporter    string   `json:"reporter"`
	MTLS        uint64   `json:"mtls"`
	Plaintext   uint64   `json:"plaintext"`
	Unknown     uint64   `json:"unknown"`
	Requests    uint64   `json:"requests"`
	Connections uint64   `json:"connections"`
	MTLSShare   *float64 `json:"mtlsShare,omitempty"`
}

type meshCallerDTO struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Units     uint64 `json:"units"`
}

// meshWorkloadPostureDTO is one workload's declared policy beside what its
// proxy observed, and the verdict the fold drew from the two. Every optional
// field is absent when its half was not read: a workload with no `observed`
// was not reported by any proxy, one with no `declaredMode` has no policy the
// cluster knows of — and neither is rendered as the other.
type meshWorkloadPostureDTO struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Service is the traced service name, when the workload has one, so the
	// row links to the same page the map does.
	Service          string           `json:"service,omitempty"`
	Observed         *meshObservedDTO `json:"observed,omitempty"`
	DeclaredMode     string           `json:"declaredMode,omitempty"`
	DeclaredScope    string           `json:"declaredScope,omitempty"`
	Posture          string           `json:"posture"`
	PlaintextCallers []meshCallerDTO  `json:"plaintextCallers,omitempty"`
	Findings         []meshFindingDTO `json:"findings,omitempty"`
}

type meshEdgeSecurityDTO struct {
	SourceNamespace string   `json:"sourceNamespace"`
	Source          string   `json:"source"`
	TargetNamespace string   `json:"targetNamespace"`
	Target          string   `json:"target"`
	Reporter        string   `json:"reporter"`
	MTLS            uint64   `json:"mtls"`
	Plaintext       uint64   `json:"plaintext"`
	Unknown         uint64   `json:"unknown"`
	MTLSShare       *float64 `json:"mtlsShare,omitempty"`
}

type meshTargetsDTO struct {
	Up    uint64   `json:"up"`
	Total uint64   `json:"total"`
	Down  []string `json:"down,omitempty"`
}

// meshSecurityResponse leads with `available`, as the control plane does: a
// data plane nobody scrapes reports zero plaintext, which reads as a fully
// encrypted mesh — the exact lie this route exists to prevent.
type meshSecurityResponse struct {
	Available bool            `json:"available"`
	State     string          `json:"state"`
	Reason    string          `json:"reason,omitempty"`
	LastSeen  *time.Time      `json:"lastSeen,omitempty"`
	Targets   *meshTargetsDTO `json:"targets,omitempty"`
	// Declared says whether the configuration half was read at all. False
	// makes every posture observed-only, and no row carries a declared mode.
	Declared  bool                     `json:"declared"`
	Workloads []meshWorkloadPostureDTO `json:"workloads"`
	Edges     []meshEdgeSecurityDTO    `json:"edges"`
	// Findings is the flat list of every posture finding, so a screen can
	// lead with what needs attention.
	Findings []meshFindingDTO `json:"findings"`
}

// handleMeshSecurity joins what the cluster declares about mutual TLS with
// what the proxies reported, per workload and per edge.
//
// The join lives here, in the API layer, like every other join between
// telemetry and configuration. Storage answers what was observed and the
// config module answers what was declared; only this layer holds both.
func (a *API) handleMeshSecurity(w http.ResponseWriter, r *http.Request) error {
	if !a.modules.Enabled(modules.InfraMetrics) {
		writeJSON(w, http.StatusOK, meshSecurityResponse{
			State:     string(storage.MeshControlPlaneUnconfigured),
			Reason:    "data-plane metrics are stored by the infra-metrics module, which is not enabled on this install",
			Workloads: []meshWorkloadPostureDTO{}, Edges: []meshEdgeSecurityDTO{}, Findings: []meshFindingDTO{},
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
	q := storage.ServiceQuery{
		Tenant: tenant, Tenants: tenants, Range: tr,
		ExcludeAux:       !parseBool(r, "includeAux", false),
		MeshDataplaneJob: a.cfg.MeshDataplaneJob,
	}
	sec, err := store.MeshSecurity(r.Context(), q)
	if err != nil {
		return err
	}
	resp := meshSecurityResponse{
		Available: sec.Available, State: string(sec.State),
		Workloads: []meshWorkloadPostureDTO{}, Edges: []meshEdgeSecurityDTO{}, Findings: []meshFindingDTO{},
	}
	if !sec.LastSeen.IsZero() {
		seen := sec.LastSeen
		resp.LastSeen = &seen
	}
	if sec.TargetsTotal > 0 {
		resp.Targets = &meshTargetsDTO{Up: sec.TargetsUp, Total: sec.TargetsTotal, Down: sec.TargetsDown}
	}
	if !sec.Available {
		resp.Reason = meshDataplaneReason(sec.State, sec.TargetsUp, sec.TargetsTotal, sec.TargetsDown)
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	declared := a.declaredMTLS(r)
	resp.Declared = declared.Declared()

	// Best-effort decoration: the service link and the "has traffic" half of
	// the uncarried verdict. A failed read costs those, never the response.
	traced := a.tracedWorkloads(r, store, q)

	observedKeys := map[nsWorkloadKey]bool{}
	for _, wl := range sec.Workloads {
		key := nsWorkloadKey{wl.Namespace, wl.Workload}
		observedKeys[key] = true
		o := observe(wl)
		row := meshWorkloadPostureDTO{
			Namespace: wl.Namespace, Name: wl.Workload, Service: traced.service[key],
			Observed: &meshObservedDTO{
				Reporter: o.reporter, MTLS: o.mtls, Plaintext: o.plaintext, Unknown: o.unknown,
				Requests: o.requests, Connections: o.connections, MTLSShare: o.mtlsShare(),
			},
		}
		for _, c := range wl.PlaintextCallers {
			row.PlaintextCallers = append(row.PlaintextCallers, meshCallerDTO{Namespace: c.Namespace, Name: c.Workload, Units: c.Units})
		}
		in := postureInput{
			namespace: wl.Namespace, workload: wl.Workload, declared: declared.Declared(),
			dataplane: declared.DataplaneMode(wl.Namespace), obs: o, hasTraffic: traced.hasTraffic[key],
		}
		if in.declared {
			row.DeclaredMode, row.DeclaredScope = declared.EffectiveMTLS(wl.Namespace, wl.Workload)
			in.declaredMode = row.DeclaredMode
		}
		resp.Workloads = append(resp.Workloads, finishPosture(row, in))
	}
	// Workloads the traces saw and no proxy reported. Only an ambient
	// namespace makes that a verdict, and the fold decides; here the rows are
	// merely offered to it.
	if declared.Declared() {
		for key := range traced.hasTraffic {
			if observedKeys[key] {
				continue
			}
			in := postureInput{
				namespace: key.ns, workload: key.workload, declared: true,
				dataplane: declared.DataplaneMode(key.ns), hasTraffic: true,
			}
			if verdict, _ := posture(in); verdict != postureUncarried {
				continue
			}
			row := meshWorkloadPostureDTO{Namespace: key.ns, Name: key.workload, Service: traced.service[key]}
			row.DeclaredMode, row.DeclaredScope = declared.EffectiveMTLS(key.ns, key.workload)
			resp.Workloads = append(resp.Workloads, finishPosture(row, in))
		}
	}
	sort.Slice(resp.Workloads, func(i, j int) bool {
		a, b := resp.Workloads[i], resp.Workloads[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	for _, row := range resp.Workloads {
		resp.Findings = append(resp.Findings, row.Findings...)
	}

	for _, e := range sec.Edges {
		o := observe(storage.MeshWorkloadSecurity{Counts: e.Counts})
		resp.Edges = append(resp.Edges, meshEdgeSecurityDTO{
			SourceNamespace: e.SourceNamespace, Source: e.Source,
			TargetNamespace: e.TargetNamespace, Target: e.Target, Reporter: e.Reporter,
			MTLS: o.mtls, Plaintext: o.plaintext, Unknown: o.unknown, MTLSShare: o.mtlsShare(),
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// finishPosture runs the fold for one row and attaches what it says.
func finishPosture(row meshWorkloadPostureDTO, in postureInput) meshWorkloadPostureDTO {
	verdict, finding := posture(in)
	row.Posture = verdict
	if finding != nil {
		row.Findings = toFindingDTOs([]meshconfig.Finding{*finding})
	}
	return row
}

// declaredMTLS is the configuration half, or an adapter that says it was not
// read: with the config module off there is no reader, and the fold must not
// compare against a policy nobody looked at.
func (a *API) declaredMTLS(r *http.Request) declaredMTLS {
	if !a.modules.Enabled(modules.MeshConfig) {
		return newSnapshotDeclared(meshconfig.NoopReader{}.Snapshot(r.Context()))
	}
	return newSnapshotDeclared(a.meshConfig().Snapshot(r.Context()))
}

// tracedWorkloads is the trace-derived half the security rows borrow: which
// workloads received requests in the window, keyed the mesh's way, and the
// service name each maps back to.
type tracedWorkloads struct {
	hasTraffic map[nsWorkloadKey]bool
	service    map[nsWorkloadKey]string
}

// tracedWorkloads reads the services and their namespaces. Proxies are left
// out: a waypoint's own spans are not traffic a proxy would report carrying
// TO it, and counting them would flag every waypoint as uncarried.
func (a *API) tracedWorkloads(r *http.Request, store storage.Store, q storage.ServiceQuery) tracedWorkloads {
	out := tracedWorkloads{hasTraffic: map[nsWorkloadKey]bool{}, service: map[nsWorkloadKey]string{}}
	services, err := store.ListServices(r.Context(), q)
	if err != nil {
		slog.Warn("mesh security: listing services failed; rows carry no service link", "error", err)
		return out
	}
	labels, err := store.ServiceLabels(r.Context(), q)
	if err != nil {
		slog.Warn("mesh security: reading service labels failed; rows carry no service link", "error", err)
		return out
	}
	namespaces := serviceNamespaces(labels)
	out.service = servicesByWorkload(services, namespaces)
	cls := a.topologyClassifier().WithEvidence(topology.LabelledTransport(services))
	for _, s := range services {
		ns := namespaces[s.Name]
		if ns == "" || s.SpanCount == 0 || cls.IsTransport(s.Name) {
			continue
		}
		ns, wl := workloadKey(s.Name, ns)
		out.hasTraffic[nsWorkloadKey{ns, wl}] = true
	}
	return out
}

// meshDataplaneReason turns a silent data plane into an instruction — the
// sibling of meshUnavailableReason, with the data plane's own three fixes.
func meshDataplaneReason(state storage.MeshScrapeState, up, total uint64, down []string) string {
	switch state {
	case storage.MeshControlPlaneUnreachable:
		named := ""
		if len(down) > 0 {
			named = " (" + strings.Join(down, ", ") + ")"
		}
		return "the data-plane scrape is running and " + countPhrase(total-up, total) +
			" not answering" + named + " — check port 15020 on the proxy pods"
	case storage.MeshControlPlaneUnrecognised:
		return "the proxies answered and none of the metrics this product reads came back. The data-plane view is Istio-shaped (istio_requests_total, istio_tcp_*): a different data plane will show its proxies on this screen but not what they carried"
	default:
		return "no data-plane metrics in this window — the sensor scrapes the proxies when mesh.dataPlane.enabled is on (the default with the mesh module) and the proxy pods carry the mesh's prometheus.io annotations"
	}
}

func countPhrase(down, total uint64) string {
	n := strconv.FormatUint(total, 10)
	switch {
	case total == 0:
		return "its targets are"
	case down == 1:
		return "1 of its " + n + " proxy targets is"
	default:
		return strconv.FormatUint(down, 10) + " of its " + n + " proxy targets are"
	}
}
