package api

import (
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Mode filters a client can ask the workload list for, beyond the two
// data-plane modes. "none" is a workload the mesh does not have;
// "declared-only" is the enrolment gap: asked for, running, and neither
// injected nor captured — the row this whole surface exists to show.
const (
	workloadModeNone         = "none"
	workloadModeDeclaredOnly = "declared-only"
)

// meshDeclaredMTLSDTO is the PeerAuthentication mode that applies to a
// workload, with the scope that decided it and the policy by name — so a
// PERMISSIVE row inside a STRICT namespace reads as a selector policy rather
// than as a bug.
type meshDeclaredMTLSDTO struct {
	Mode   string `json:"mode"`
	Source string `json:"source"`
	Policy string `json:"policy"`
}

// meshObservedMTLSDTO is what the data plane reported for a workload's own
// traffic. Declared now so the wire shape is settled; nothing fills it in this
// build, and a client reading it absent must render "not measured", never 0%.
type meshObservedMTLSDTO struct {
	MTLSShare *float64 `json:"mtlsShare,omitempty"`
	Plaintext uint64   `json:"plaintext"`
	MTLS      uint64   `json:"mtls"`
	Unknown   uint64   `json:"unknown"`
	Reporter  string   `json:"reporter,omitempty"`
}

// meshPolicyRefDTO names one policy that covers a workload and at what scope
// it reached it. Findings ride only on the single-workload response: they are
// the policy's own, and a list of workloads is not the place to repeat them.
type meshPolicyRefDTO struct {
	Kind      string           `json:"kind"`
	Namespace string           `json:"namespace"`
	Name      string           `json:"name"`
	Scope     string           `json:"scope"`
	Findings  []meshFindingDTO `json:"findings,omitempty"`
}

// meshWorkloadDTO is one thing the cluster runs, from CONFIGURATION, with
// whatever telemetry we also have for it.
//
// The row exists whether or not the workload ever sent a span, which is the
// point: a workload asked into the mesh and never enrolled produces no traffic
// of its own, and is the most common way an ambient mesh is misconfigured.
type meshWorkloadDTO struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	// DeclaredMode is what the labels asked for; DataplaneMode is what the
	// pods say happened. Both absent means out of mesh, and absent must not
	// render as a mode.
	DeclaredMode  string `json:"declaredMode,omitempty"`
	DataplaneMode string `json:"dataplaneMode,omitempty"`
	Injected      bool   `json:"injected"`
	Captured      bool   `json:"captured"`
	// WaypointNamespace is only sent when it differs from Namespace, so the
	// common case stays quiet.
	Waypoint          string `json:"waypoint,omitempty"`
	WaypointNamespace string `json:"waypointNamespace,omitempty"`
	WaypointSource    string `json:"waypointSource,omitempty"`
	ServiceAccount    string `json:"serviceAccount,omitempty"`
	Pods              int    `json:"pods"`
	RunningPods       int    `json:"runningPods"`
	// DeclaredMTLS is absent when no policy applies and the mesh default
	// governs — which we did not read, and will not guess. ObservedMTLS is
	// absent when the data plane was not asked: not measured is not zero.
	DeclaredMTLS *meshDeclaredMTLSDTO `json:"declaredMtls,omitempty"`
	ObservedMTLS *meshObservedMTLSDTO `json:"observedMtls,omitempty"`
	// HasTraffic says telemetry saw this workload in the window; the rates
	// are pointers so a silent workload carries no number at all.
	HasTraffic bool               `json:"hasTraffic"`
	RatePerSec *float64           `json:"ratePerSec,omitempty"`
	ErrorRate  *float64           `json:"errorRate,omitempty"`
	Services   []string           `json:"services"`
	Policies   []meshPolicyRefDTO `json:"policies"`
	Errors     int                `json:"errors"`
	Warnings   int                `json:"warnings"`
}

type meshWorkloadsResponse struct {
	// State and Reason lead, as on every mesh-config response: the rows are
	// meaningless if the cluster was not read.
	State         string            `json:"state"`
	Reason        string            `json:"reason,omitempty"`
	SyncedAt      *string           `json:"syncedAt,omitempty"`
	MissingKinds  []string          `json:"missingKinds,omitempty"`
	Truncated     bool              `json:"truncated,omitempty"`
	PodsTruncated bool              `json:"podsTruncated,omitempty"`
	ChecksSkipped string            `json:"checksSkipped,omitempty"`
	Kinds         []meshKindSyncDTO `json:"kinds,omitempty"`
	Workloads     []meshWorkloadDTO `json:"workloads"`
}

// handleMeshWorkloads lists workloads as the CLUSTER runs them, joined to what
// telemetry saw. Scoping is the namespaces route's, exactly: the roster is
// cluster-wide, the telemetry decoration is project-scoped and best-effort.
func (a *API) handleMeshWorkloads(w http.ResponseWriter, r *http.Request) error {
	mode := r.URL.Query().Get("mode")
	if !validWorkloadMode(mode) {
		return badRequest("mode must be one of ambient, sidecar, %s or %s", workloadModeNone, workloadModeDeclaredOnly)
	}
	snap := a.meshConfig().Snapshot(r.Context())
	resp := meshWorkloadsResponse{
		State:         string(snap.State),
		Reason:        snap.Reason,
		SyncedAt:      syncedAtString(snap.SyncedAt),
		MissingKinds:  snap.MissingKinds,
		Truncated:     snap.Truncated,
		PodsTruncated: snap.PodsTruncated,
		Workloads:     []meshWorkloadDTO{},
	}
	if snap.State != meshconfig.StateOK {
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.ChecksSkipped = checksSkipped(snap)
	resp.Kinds = toKindSyncDTOs(snap.Kinds)
	if !podsReadable(snap) {
		// A workload is a fact about pods. With none readable the list is
		// empty and ChecksSkipped says why, rather than an empty list that
		// reads as a cluster running nothing.
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	traffic := a.workloadTraffic(r)
	wantNS := r.URL.Query().Get("namespace")
	for _, wl := range snap.Workloads {
		if wantNS != "" && wl.Namespace != wantNS {
			continue
		}
		if !matchesWorkloadMode(wl, mode) {
			continue
		}
		resp.Workloads = append(resp.Workloads, toWorkloadDTO(wl, traffic))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func validWorkloadMode(mode string) bool {
	switch mode {
	case "", meshconfig.DataplaneAmbient, meshconfig.DataplaneSidecar, workloadModeNone, workloadModeDeclaredOnly:
		return true
	}
	return false
}

// matchesWorkloadMode filters on what the pods SAY, not on what the labels
// asked for — except for declared-only, which is precisely the gap between
// the two.
func matchesWorkloadMode(wl meshconfig.Workload, mode string) bool {
	switch mode {
	case "":
		return true
	case workloadModeNone:
		return wl.DataplaneMode == ""
	case workloadModeDeclaredOnly:
		return wl.DeclaredMode != "" && !wl.Injected && !wl.Captured
	default:
		return wl.DataplaneMode == mode
	}
}

// workloadTraffic is the telemetry half of the join, keyed "namespace/name".
//
// Best-effort, exactly as the namespaces route treats it: a failed store read
// costs the traffic columns, never the rows. Telemetry keys on the service
// name the sensor derives and configuration on the workload's, so the join
// key is (namespace, name) with the service name's trailing ".namespace"
// stripped where the sensor added one.
func (a *API) workloadTraffic(r *http.Request) map[string]serviceDTO {
	out := map[string]serviceDTO{}
	store, err := a.store()
	if err != nil {
		return out
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return out
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return out
	}
	q := storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr, ExcludeAux: true}
	services, err := store.ListServices(r.Context(), q)
	if err != nil {
		return out
	}
	labels, err := store.ServiceLabels(r.Context(), q)
	if err != nil {
		return out
	}
	namespaces := serviceNamespaces(labels)
	window := tr.End.Sub(tr.Start)
	for _, s := range services {
		ns := namespaces[s.Name]
		if ns == "" {
			// No namespace, no join: a service that declares none cannot be
			// matched to a workload without guessing where it lives.
			continue
		}
		out[ns+"/"+stripNamespaceSuffix(s.Name, ns)] = toServiceDTO(s, window)
	}
	return out
}

// stripNamespaceSuffix removes a trailing "."+namespace from a service name,
// which is how the sensor spells a workload it could only name by DNS.
func stripNamespaceSuffix(service, namespace string) string {
	if namespace == "" {
		return service
	}
	return strings.TrimSuffix(service, "."+namespace)
}

func toWorkloadDTO(wl meshconfig.Workload, traffic map[string]serviceDTO) meshWorkloadDTO {
	row := meshWorkloadDTO{
		Namespace:      wl.Namespace,
		Name:           wl.Name,
		Kind:           wl.Kind,
		DeclaredMode:   wl.DeclaredMode,
		DataplaneMode:  wl.DataplaneMode,
		Injected:       wl.Injected,
		Captured:       wl.Captured,
		Waypoint:       wl.Waypoint,
		WaypointSource: wl.WaypointSource,
		ServiceAccount: wl.ServiceAccount,
		Pods:           wl.Pods,
		RunningPods:    wl.RunningPods,
		Services:       []string{},
		Policies:       []meshPolicyRefDTO{},
	}
	if wl.WaypointNamespace != "" && wl.WaypointNamespace != wl.Namespace {
		row.WaypointNamespace = wl.WaypointNamespace
	}
	if wl.DeclaredMTLS.Mode != "" {
		row.DeclaredMTLS = &meshDeclaredMTLSDTO{
			Mode: wl.DeclaredMTLS.Mode, Source: wl.DeclaredMTLS.Source, Policy: wl.DeclaredMTLS.Policy,
		}
	}
	if t, ok := traffic[wl.Namespace+"/"+wl.Name]; ok {
		rate, errRate := t.RatePerSec, t.ErrorRate
		row.HasTraffic, row.RatePerSec, row.ErrorRate = true, &rate, &errRate
	}
	row.Services = append(row.Services, wl.Services...)
	for _, p := range wl.Policies {
		row.Policies = append(row.Policies, meshPolicyRefDTO{Kind: p.Kind, Namespace: p.Namespace, Name: p.Name, Scope: p.Scope})
	}
	for _, f := range wl.Findings {
		switch f.Severity {
		case meshconfig.SeverityError:
			row.Errors++
		case meshconfig.SeverityWarning:
			row.Warnings++
		}
	}
	return row
}
