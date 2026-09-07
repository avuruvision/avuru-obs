package api

import (
	"log/slog"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// meshObservations is the data plane's account of every workload it reported,
// keyed the mesh's way, for the screens that list workloads from CONFIGURATION.
//
// The Security tab starts from what the proxies saw and asks what was
// declared; the Workloads tab starts from what the cluster declared and asks
// what the proxies saw. Same two halves, same fold — this type carries the
// observed half to the second screen so the two can never disagree about one
// workload.
type meshObservations struct {
	byKey map[nsWorkloadKey]storage.MeshWorkloadSecurity
	// available is whether the data plane was read at all. Without it a
	// workload the proxies did not report is not "uncarried" — nobody looked.
	available bool
}

// observedWorkloads reads the data plane, best-effort: the roster is complete
// without it, and a failed read costs the observed column, never the rows.
// Nothing is read at all without infra-metrics, where the tables do not exist.
func (a *API) observedWorkloads(r *http.Request) meshObservations {
	out := meshObservations{byKey: map[nsWorkloadKey]storage.MeshWorkloadSecurity{}}
	if !a.modules.Enabled(modules.InfraMetrics) {
		return out
	}
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
	q := storage.ServiceQuery{
		Tenant: tenant, Tenants: tenants, Range: tr, ExcludeAux: true,
		MeshDataplaneJob: a.cfg.MeshDataplaneJob,
	}
	sec, err := store.MeshSecurity(r.Context(), q)
	if err != nil {
		slog.Warn("mesh workloads: reading the data plane failed; rows carry no observed mTLS", "error", err)
		return out
	}
	out.available = sec.Available
	for _, wl := range sec.Workloads {
		out.byKey[nsWorkloadKey{wl.Namespace, wl.Workload}] = wl
	}
	return out
}

// decorate attaches the observed half and the posture to a workload row, and
// returns the posture finding when the fold produced one.
//
// The row's own findings came from the validator; the posture finding is the
// join's, and it is counted on the row the same way so an "Issues" column
// means one thing.
func (o meshObservations) decorate(row *meshWorkloadDTO, wl meshconfig.Workload, snap meshconfig.Snapshot) *meshconfig.Finding {
	in := postureInput{
		namespace: wl.Namespace, workload: wl.Name, declared: true,
		declaredMode: wl.DeclaredMTLS.Mode, dataplane: snap.DataplaneMode(wl.Namespace),
		hasTraffic: row.HasTraffic && o.available,
	}
	if sec, ok := o.byKey[nsWorkloadKey{wl.Namespace, wl.Name}]; ok {
		obs := observe(sec)
		in.obs = obs
		row.ObservedMTLS = &meshObservedMTLSDTO{
			MTLSShare: obs.mtlsShare(), Plaintext: obs.plaintext, MTLS: obs.mtls, Unknown: obs.unknown,
			Reporter: obs.reporter,
		}
	}
	verdict, finding := posture(in)
	row.Posture = verdict
	if finding != nil {
		switch finding.Severity {
		case meshconfig.SeverityError:
			row.Errors++
		case meshconfig.SeverityWarning:
			row.Warnings++
		}
	}
	return finding
}

// postureCounts rolls the posture findings up per namespace, so the namespace
// list counts what the join found beside what the validator found.
func (o meshObservations) postureCounts(snap meshconfig.Snapshot, traffic map[string]serviceDTO) map[string]severityCount {
	out := map[string]severityCount{}
	for _, wl := range snap.Workloads {
		row := meshWorkloadDTO{}
		_, row.HasTraffic = traffic[wl.Namespace+"/"+wl.Name]
		f := o.decorate(&row, wl, snap)
		if f == nil {
			continue
		}
		c := out[wl.Namespace]
		switch f.Severity {
		case meshconfig.SeverityError:
			c.errors++
		case meshconfig.SeverityWarning:
			c.warnings++
		}
		out[wl.Namespace] = c
	}
	return out
}
