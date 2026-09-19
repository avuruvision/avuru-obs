package api

import (
	"fmt"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// meshSnapshot is the cluster's mesh configuration as THIS caller may see it.
//
// The snapshot is read from the Kubernetes API, which has no tenant column:
// every namespace, workload and policy on the cluster is in it. Every other
// screen scopes what it shows to the request's project through the store's
// Tenant filter, and the mesh routes must draw the same line — the shared demo
// viewer, granted one project, was reading the whole cluster's configuration
// through them.
//
// The line is the identity's, not the project's. An identity that may see
// every project ("*" grant, or authentication off) is the cluster operator,
// and gets the cluster whole: the module's reason to exist for them is the
// namespace whose telemetry never arrived, and narrowing to telemetry would
// hide exactly that row. An identity granted specific projects sees the
// cluster where those projects' telemetry reaches — the same namespaces its
// service map shows, derived the same way — and nothing else.
//
// Fails closed: when the project's namespaces cannot be resolved, the answer
// is the error, never the cluster and never a silent empty list that would
// read as "your project has no mesh".
func (a *API) meshSnapshot(r *http.Request) (meshconfig.Snapshot, error) {
	snap := a.meshConfig().Snapshot(r.Context())
	id := identityFrom(r.Context())
	if id == nil || id.HasWildcard() {
		return snap, nil
	}
	if snap.State != meshconfig.StateOK {
		// Nothing to narrow, and the reason must reach the caller.
		return snap, nil
	}
	keep, err := a.projectNamespaces(r)
	if err != nil {
		return meshconfig.Snapshot{}, err
	}
	return snap.Narrow(keep), nil
}

// projectNamespaces is the set of namespaces the request's project reaches:
// the ones its telemetry reported in the window, read through the same
// tenant-filtered labels the service map and the namespaces route's own
// counts already use. One join, one definition of "the project's namespaces".
func (a *API) projectNamespaces(r *http.Request) (map[string]bool, error) {
	store, err := a.store()
	if err != nil {
		return nil, err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return nil, err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	labels, err := store.ServiceLabels(r.Context(), storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr})
	if err != nil {
		return nil, fmt.Errorf("resolving the project's namespaces: %w", err)
	}
	keep := map[string]bool{}
	for _, ns := range serviceNamespaces(labels) {
		keep[ns] = true
	}
	return keep, nil
}
