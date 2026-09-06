package api

import (
	"net/http"
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
	// not guess.
	MTLSMode string `json:"mtlsMode,omitempty"`
	// Services counts the workloads telemetry saw here in the window. Zero is
	// meaningful and is the point: a configured namespace with no traffic.
	Services int `json:"services"`
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
	MissingKinds []string           `json:"missingKinds,omitempty"`
	Truncated    bool               `json:"truncated,omitempty"`
	Namespaces   []meshNamespaceDTO `json:"namespaces"`
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
		State:        string(snap.State),
		Reason:       snap.Reason,
		MissingKinds: snap.MissingKinds,
		Truncated:    snap.Truncated,
		Namespaces:   []meshNamespaceDTO{},
	}
	if !snap.SyncedAt.IsZero() {
		at := snap.SyncedAt.UTC().Format(time.RFC3339)
		resp.SyncedAt = &at
	}
	if snap.State != meshconfig.StateOK {
		// 200 with a stated reason, not an error: the question is legitimate on
		// this install, and the reason is the actionable part.
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

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
	for _, ns := range snap.Namespaces {
		row := meshNamespaceDTO{
			Name:          ns.Name,
			DataplaneMode: ns.DataplaneMode,
			Waypoint:      ns.Waypoint,
			MTLSMode:      ns.MTLSMode,
			Services:      counts[ns.Name],
		}
		if ns.WaypointNamespace != "" && ns.WaypointNamespace != ns.Name {
			row.WaypointNamespace = ns.WaypointNamespace
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
func findingCounts(snap meshconfig.Snapshot) map[string]severityCount {
	out := map[string]severityCount{}
	for _, o := range snap.Objects {
		for _, f := range o.Findings {
			c := out[o.Namespace]
			switch f.Severity {
			case meshconfig.SeverityError:
				c.errors++
			case meshconfig.SeverityWarning:
				c.warnings++
			}
			out[o.Namespace] = c
		}
	}
	return out
}

// meshConfig returns the configured reader, or a NoopReader when the module is
// off — so no call site needs a nil check to stay correct.
func (a *API) meshConfig() meshconfig.Reader {
	if a.cfg.MeshConfigReader == nil {
		return meshconfig.NoopReader{}
	}
	return a.cfg.MeshConfigReader
}
