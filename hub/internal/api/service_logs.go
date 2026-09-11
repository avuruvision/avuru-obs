package api

import (
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// handleServiceLogs reads one service's logs the way the workload page reads a
// workload's: the application's own lines, plus — when the service can be tied
// to a workload — what the proxies carrying it wrote about it, as one stream
// under one cursor.
//
// It exists because a service's own name is often not the name its lines are
// filed under. Traces carry the OTel service.name; container stdout is filed
// under the workload that wrote it, and the proxies' access lines under the
// proxies. Asking the store for ServiceName = service.name therefore returns
// nothing for a service whose Deployment is named differently — an empty tab
// beside a mesh page full of that same service's stack traces.
//
// Gated on the logs module alone. Without mesh or mesh-config it still answers,
// with the application's own lines and a sentence saying what is missing: a
// screen that quietly shows less is worse than one that shows less and says so.
func (a *API) handleServiceLogs(w http.ResponseWriter, r *http.Request) error {
	service := r.PathValue("service")
	if strings.TrimSpace(service) == "" {
		return badRequest("service must not be empty")
	}
	wanted, err := parseLogSources(r.URL.Query().Get("source"))
	if err != nil {
		return err
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 100)
	if err != nil {
		return err
	}
	cursor, err := parseLogCursor(r)
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}

	q := r.URL.Query()
	namespace, workload, why := q.Get("namespace"), q.Get("workload"), ""
	// The caller may already know the answer: the first page returns the
	// resolved workload and every later page echoes it back. Re-resolving per
	// page would cost a read each time AND let a snapshot refresh change the
	// source set halfway down a scroll, which the cursor cannot survive.
	if workload == "" {
		namespace, workload, why = a.resolveServiceWorkload(r, store,
			storage.ServiceQuery{Tenant: tenant, Tenants: tenants, Range: tr}, service)
	}

	var desc workloadLogSources
	if workload == "" {
		desc = workloadLogSources{meshLogSourcesDTO: meshLogSourcesDTO{
			App:                []string{service},
			Ztunnel:            []string{},
			Waypoint:           []string{},
			Needles:            []string{},
			ProxiesUnavailable: why,
		}}
	} else {
		desc = a.workloadLogSources(r, namespace, workload, q.Get("waypoint"), service)
	}

	page, err := store.SearchLogs(r.Context(), storage.LogQuery{
		Tenant: tenant, Tenants: tenants, Range: tr,
		Sources:     desc.sources(wanted),
		MinSeverity: q.Get("severity"),
		Query:       q.Get("q"),
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		return err
	}
	resp := meshWorkloadLogsResponse{
		logsResponse: logsResponse{Logs: make([]logRecordDTO, 0, len(page.Logs)), NextCursor: encodeLogCursor(page.NextCursor)},
		Sources:      desc.meshLogSourcesDTO,
	}
	for _, l := range page.Logs {
		resp.Logs = append(resp.Logs, toLogRecordDTO(l))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// resolveServiceWorkload ties an OTel service name to the workload it runs as,
// returning the namespace, the workload, and — when it cannot — one
// operator-facing sentence naming the thing to change.
//
// Two independent signals, tried in that order:
//
//  1. The service's own spans. The k8sattributes processor stamps the owning
//     Deployment on every record it touches, so this is the cluster's own
//     statement about which workload emitted the span, and it needs no mesh.
//  2. The cluster snapshot. A Kubernetes Service named like the OTel service
//     already lists the workloads its selector matches — which is what ties
//     valife-report-service to valife-report — and failing that, a workload of
//     that name outright.
//
// A read that fails is not an error here: the tab degrades to the application's
// own lines, which is what it showed before this endpoint existed.
func (a *API) resolveServiceWorkload(r *http.Request, store storage.Store, q storage.ServiceQuery, service string) (namespace, workload, why string) {
	if sw, err := store.ServiceWorkload(r.Context(), q, service); err == nil && sw.Workload != "" {
		return sw.Namespace, sw.Workload, ""
	} else if err == nil {
		namespace = sw.Namespace
	}

	noAttr := "the proxies' lines need a workload: this service's spans carry no k8s.deployment.name, which the collector's k8sattributes processor is what adds"
	if !a.modules.Enabled(modules.MeshConfig) {
		return "", "", noAttr + ", and mesh-config is off, so the Service behind this name is not known either"
	}
	snap := a.meshConfig().Snapshot(r.Context())
	if snap.State != meshconfig.StateOK {
		return "", "", noAttr + ", and the cluster was not read — " + snap.Reason
	}

	// A Service by this name: its selector already names the workloads behind
	// it. Scoped to the namespace the spans reported when they reported one;
	// otherwise only a name unique across the cluster is an answer, because a
	// namesake in two namespaces is a coin toss, not a match.
	if ns, wl, ok := uniqueMatch(len(snap.Services), namespace, service, func(i int) (string, string, string) {
		svc := snap.Services[i]
		if len(svc.Workloads) == 0 {
			// A Service with no workload behind it — an ExternalName or a
			// hand-managed endpoint set. A real answer, but not this one.
			return svc.Namespace, svc.Name, ""
		}
		return svc.Namespace, svc.Name, svc.Workloads[0]
	}); ok {
		return ns, wl, ""
	}
	if ns, wl, ok := uniqueMatch(len(snap.Workloads), namespace, service, func(i int) (string, string, string) {
		wl := snap.Workloads[i]
		return wl.Namespace, wl.Name, wl.Namespace + "/" + wl.Name
	}); ok {
		return ns, wl, ""
	}
	return "", "", noAttr + ", and no Service or workload in the cluster snapshot is named " + service
}

// uniqueMatch finds the one entry named `service` and returns the workload it
// points at. `at` yields an entry's namespace, name, and the "namespace/name"
// of the workload behind it — empty when there is none, which is not a match.
//
// The namespace the spans reported narrows the search when there is one.
// Without it only a cluster-unique name is an answer: a namesake in two
// namespaces is a coin toss, and pointing a log tab at the wrong team's pods
// is worse than admitting the name was ambiguous.
func uniqueMatch(n int, preferNS, service string, at func(int) (string, string, string)) (namespace, workload string, ok bool) {
	var found int
	for i := 0; i < n; i++ {
		ns, name, target := at(i)
		if name != service || target == "" || (preferNS != "" && ns != preferNS) {
			continue
		}
		wns, wname, cut := strings.Cut(target, "/")
		if !cut || wns == "" || wname == "" {
			continue
		}
		found++
		namespace, workload = wns, wname
	}
	if found != 1 {
		return "", "", false
	}
	return namespace, workload, true
}
