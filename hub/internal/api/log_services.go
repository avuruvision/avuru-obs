package api

import (
	"net/http"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// handleLogServices returns log-backed services and the workloads those
// services can safely resolve to. Resolution stays project and time scoped:
// a cluster snapshot alone must never make another project's workload appear
// in autocomplete results.
func (a *API) handleLogServices(w http.ResponseWriter, r *http.Request) error {
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
	sq := storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr}
	presence, err := store.ServicePresence(r.Context(), sq, []storage.Signal{storage.SignalLogs})
	if err != nil {
		return err
	}

	services := make([]string, 0, len(presence))
	workloadSet := make(map[string]bool, len(presence))
	for _, p := range presence {
		if p.LogRecords == 0 {
			continue
		}
		services = append(services, p.Name)
		namespace, workload, _ := a.resolveServiceWorkload(r, store, sq, p.Name)
		if namespace != "" && workload != "" {
			workloadSet[namespace+"/"+workload] = true
		}
	}
	sort.Strings(services)
	workloads := make([]string, 0, len(workloadSet))
	for workload := range workloadSet {
		workloads = append(workloads, workload)
	}
	sort.Strings(workloads)

	writeJSON(w, http.StatusOK, struct {
		Services  []string `json:"services"`
		Workloads []string `json:"workloads"`
	}{Services: services, Workloads: workloads})
	return nil
}
