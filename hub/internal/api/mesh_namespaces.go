package api

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// meshNamespaceDTO is one namespace's mesh membership, from CONFIGURATION, with
// whatever telemetry we also have for it.
//
// The config half is why this row can exist at all for a namespace that has
// sent nothing: enrolment is a label, and a namespace labelled and not working
// looks, through traffic alone, exactly like a namespace nobody labelled.
type meshNamespaceDTO struct {
	Name string `json:"name"`
	// DataplaneMode is "ambient", "sidecar", or absent for out of mesh. Absent
	// is a real answer and must not render as a mode.
	DataplaneMode string `json:"dataplaneMode,omitempty"`
	Waypoint      string `json:"waypoint,omitempty"`
	// WaypointNamespace is only sent when it differs from Name, so the common
	// case stays quiet.
	WaypointNamespace string `json:"waypointNamespace,omitempty"`
	// MTLSMode is the effective PeerAuthentication mode. Absent means no policy
	// applies and the mesh default governs — which we did not read, and will
	// not guess. MTLSSource says which scope decided it ("namespace" or
	// "mesh") and MTLSPolicy names the policy, so a mode can be traced to the
	// object that set it; both are absent exactly when the mode is.
	MTLSMode   string `json:"mtlsMode,omitempty"`
	MTLSSource string `json:"mtlsSource,omitempty"`
	MTLSPolicy string `json:"mtlsPolicy,omitempty"`
	// Services counts the workloads telemetry saw here in the window. Zero is
	// meaningful and is the point: a configured namespace with no traffic.
	Services int `json:"services"`
	// Workloads counts what the cluster runs here and Enrolled how many of
	// them the mesh actually has. Pointers: when pods are not readable both
	// are absent, never zero — a zero would read as "nothing runs here".
	Workloads *int `json:"workloads,omitempty"`
	Enrolled  *int `json:"enrolled,omitempty"`
	// Issues counts validation findings, by severity.
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

type meshNamespacesResponse struct {
	// State and Reason lead for the same reason `available` leads on the
	// control plane: every row below is meaningless if we could not read the
	// cluster, and a client that rendered an empty list anyway would be
	// reporting a mesh with no configuration.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// SyncedAt is when the cluster was last read. Absent when it never was.
	SyncedAt *string `json:"syncedAt,omitempty"`
	// MissingKinds are the resource types this cluster does not have or we may
	// not read. One missing type costs its own row, not the screen.
	MissingKinds []string `json:"missingKinds,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	// PodsTruncated says the pod list was cut on its own, and ChecksSkipped
	// says in one sentence why the pod-dependent checks did not run — so an
	// empty issues column can never be read as a clean bill.
	PodsTruncated bool   `json:"podsTruncated,omitempty"`
	ChecksSkipped string `json:"checksSkipped,omitempty"`
	// Kinds describes each cache the snapshot was read from, so a screen can
	// say which one is stale and which one was cut.
	Kinds      []meshKindSyncDTO  `json:"kinds,omitempty"`
	Namespaces []meshNamespaceDTO `json:"namespaces"`
}

// meshKindSyncDTO is one kind's cache: how much it holds, when it was warm,
// when it last moved, and whether the snapshot had to cut it.
type meshKindSyncDTO struct {
	Kind         string  `json:"kind"`
	Count        int     `json:"count"`
	SyncedAt     *string `json:"syncedAt,omitempty"`
	LastChangeAt *string `json:"lastChangeAt,omitempty"`
	Truncated    bool    `json:"truncated,omitempty"`
}

// handleMeshNamespaces lists namespaces as the CLUSTER defines them, joined to
// what telemetry saw.
//
// The join is here, in the API layer, exactly where stampServiceNamespaces
// already does the same job — never in the storage or config packages, which
// must each stay answerable on their own.
func (a *API) handleMeshNamespaces(w http.ResponseWriter, r *http.Request) error {
	snap := a.meshConfig().Snapshot(r.Context())

	resp := meshNamespacesResponse{
		State:         string(snap.State),
		Reason:        snap.Reason,
		SyncedAt:      syncedAtString(snap.SyncedAt),
		MissingKinds:  snap.MissingKinds,
		Truncated:     snap.Truncated,
		PodsTruncated: snap.PodsTruncated,
		Namespaces:    []meshNamespaceDTO{},
	}
	if snap.State != meshconfig.StateOK {
		// 200 with a stated reason, not an error: the question is legitimate on
		// this install, and the reason is the actionable part.
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	resp.ChecksSkipped = checksSkipped(snap)
	resp.Kinds = toKindSyncDTOs(snap.Kinds)

	// Telemetry is best-effort decoration on a list that is complete without
	// it. A failed read costs the counts, never the roster — which is the whole
	// asymmetry this module exists to create.
	counts := map[string]int{}
	if store, err := a.store(); err == nil {
		if tr, terr := parseTimeRange(r); terr == nil {
			if tenant, tenants, aerr := a.projectTenants(r, auth.RoleViewer); aerr == nil {
				q := storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr, ExcludeAux: true}
				if labels, lerr := store.ServiceLabels(r.Context(), q); lerr == nil {
					for _, ns := range serviceNamespaces(labels) {
						counts[ns]++
					}
				}
			}
		}
	}

	findings := findingCounts(snap)
	for ns, c := range a.observedWorkloads(r).postureCounts(snap, a.workloadTraffic(r)) {
		f := findings[ns]
		f.errors, f.warnings = f.errors+c.errors, f.warnings+c.warnings
		findings[ns] = f
	}
	podsOK := podsReadable(snap)
	for _, ns := range snap.Namespaces {
		row := meshNamespaceDTO{
			Name:          ns.Name,
			DataplaneMode: ns.DataplaneMode,
			Waypoint:      ns.Waypoint,
			MTLSMode:      ns.MTLSMode,
			MTLSSource:    ns.MTLSSource,
			MTLSPolicy:    ns.MTLSPolicy,
			Services:      counts[ns.Name],
		}
		if ns.WaypointNamespace != "" && ns.WaypointNamespace != ns.Name {
			row.WaypointNamespace = ns.WaypointNamespace
		}
		if podsOK {
			workloads, enrolled := ns.Workloads, ns.Enrolled
			row.Workloads, row.Enrolled = &workloads, &enrolled
		}
		row.Errors, row.Warnings = findings[ns.Name].errors, findings[ns.Name].warnings
		resp.Namespaces = append(resp.Namespaces, row)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type severityCount struct{ errors, warnings int }

// findingCounts rolls validation findings up per namespace, which is what makes
// a long namespace list scannable: the row that needs attention says so.
//
// Objects, namespaces and workloads each carry their own findings, and all
// three land on the namespace row: a workload the node agent never captured
// is a problem with the namespace that asked for it.
func findingCounts(snap meshconfig.Snapshot) map[string]severityCount {
	out := map[string]severityCount{}
	add := func(namespace string, findings []meshconfig.Finding) {
		for _, f := range findings {
			c := out[namespace]
			switch f.Severity {
			case meshconfig.SeverityError:
				c.errors++
			case meshconfig.SeverityWarning:
				c.warnings++
			}
			out[namespace] = c
		}
	}
	for _, o := range snap.Objects {
		add(o.Namespace, o.Findings)
	}
	for _, ns := range snap.Namespaces {
		add(ns.Name, ns.Findings)
	}
	for _, wl := range snap.Workloads {
		add(wl.Namespace, wl.Findings)
	}
	return out
}

// podsReadable reports whether the snapshot could read pods at all. Every
// number derived from pods — workload counts, enrolment, the workload list —
// must be absent rather than zero when it could not.
func podsReadable(snap meshconfig.Snapshot) bool {
	return !slices.Contains(snap.MissingKinds, meshconfig.KindPod)
}

// checksSkipped says why the pod-dependent checks did not run, and what to do.
//
// The validator's own sentence names the checks it skipped; the two fallbacks
// below cover a snapshot that never reached it.
func checksSkipped(snap meshconfig.Snapshot) string {
	switch {
	case snap.ChecksSkipped != "":
		return snap.ChecksSkipped
	case !podsReadable(snap):
		return "pods are not readable — grant pods get/list/watch in the mesh-config ClusterRole"
	case snap.PodsTruncated:
		total := len(snap.Pods)
		for _, k := range snap.Kinds {
			if k.Kind == meshconfig.KindPod {
				total = k.Count
			}
		}
		return fmt.Sprintf("the snapshot keeps the first %d of the cluster's %d pods, so workloads past the cut are not listed "+
			"and the checks that need every pod did not run — an empty issues column here is not a clean bill", len(snap.Pods), total)
	}
	return ""
}

func toKindSyncDTOs(kinds []meshconfig.KindSync) []meshKindSyncDTO {
	if len(kinds) == 0 {
		return nil
	}
	out := make([]meshKindSyncDTO, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, meshKindSyncDTO{
			Kind:         k.Kind,
			Count:        k.Count,
			SyncedAt:     syncedAtString(k.SyncedAt),
			LastChangeAt: syncedAtString(k.LastChangeAt),
			Truncated:    k.Truncated,
		})
	}
	return out
}

// syncedAtString formats a time for the wire, or returns nil for a zero one — a
// read that never happened must not carry a timestamp.
func syncedAtString(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// meshConfig returns the configured reader, or a NoopReader when the module is
// off — so no call site needs a nil check to stay correct.
func (a *API) meshConfig() meshconfig.Reader {
	if a.cfg.MeshConfigReader == nil {
		return meshconfig.NoopReader{}
	}
	return a.cfg.MeshConfigReader
}
