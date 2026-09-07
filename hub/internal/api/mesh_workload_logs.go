package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The three sources a workload's logs come from. The application's own
// lines; the node proxy's access lines, which name the pods; the waypoint's,
// which name the Service.
const (
	logSourceApp      = "app"
	logSourceZtunnel  = "ztunnel"
	logSourceWaypoint = "waypoint"
)

// ztunnelServiceNames is how the sensor files the node proxy's lines: the
// DaemonSet's name. A revisioned install names it ztunnel-<rev>; extending
// this list is the change for that.
var ztunnelServiceNames = []string{"ztunnel"}

// maxLogNeedles bounds the pod names a ztunnel or waypoint branch is
// narrowed to. Past it the branch falls back to the name conjunction rather
// than shipping a thousand-entry list to the store.
const maxLogNeedles = 200

// meshLogSourcesDTO says what was actually asked of the store, so an empty
// column is never silent: the service names per source, the needles a proxy
// line had to contain, and whether the pods came from the snapshot.
type meshLogSourcesDTO struct {
	App      []string `json:"app"`
	Ztunnel  []string `json:"ztunnel"`
	Waypoint []string `json:"waypoint"`
	Needles  []string `json:"needles"`
	Precise  bool     `json:"precise"`
	// Fallback names why the pods could not be read, when they could not,
	// in the operator's terms.
	Fallback string `json:"fallback,omitempty"`
}

type meshWorkloadLogsResponse struct {
	logsResponse
	Sources meshLogSourcesDTO `json:"sources"`
}

// handleMeshWorkloadLogs reads one workload's logs from all three sources as
// one stream under one cursor. The composition lives here because only the
// hub knows the pods behind a workload and the waypoint it is bound to.
func (a *API) handleMeshWorkloadLogs(w http.ResponseWriter, r *http.Request) error {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
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

	desc := a.workloadLogSources(r, namespace, name, r.URL.Query().Get("waypoint"))
	page, err := store.SearchLogs(r.Context(), storage.LogQuery{
		Tenant: tenant, Tenants: tenants, Range: tr,
		Sources:     desc.sources(wanted),
		MinSeverity: r.URL.Query().Get("severity"),
		Query:       r.URL.Query().Get("q"),
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

// parseLogSources reads the source= list; empty means all three.
func parseLogSources(raw string) (map[string]bool, error) {
	all := map[string]bool{logSourceApp: true, logSourceZtunnel: true, logSourceWaypoint: true}
	if strings.TrimSpace(raw) == "" {
		return all, nil
	}
	out := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if !all[s] {
			return nil, badRequest("source must be a comma list of %s, %s and %s", logSourceApp, logSourceZtunnel, logSourceWaypoint)
		}
		out[s] = true
	}
	return out, nil
}

// workloadLogSources is the descriptor plus the needle lists it was built
// from: precise, one list of pod names and the Service host; fallback, two
// conjunctions that keep a name apart from its namesake elsewhere.
type workloadLogSources struct {
	meshLogSourcesDTO
	bodyAll [][]string
}

func (s workloadLogSources) sources(wanted map[string]bool) []storage.LogSource {
	var out []storage.LogSource
	if wanted[logSourceApp] {
		out = append(out, storage.LogSource{Services: s.App})
	}
	if wanted[logSourceZtunnel] {
		out = append(out, storage.LogSource{Services: s.Ztunnel, BodyAll: s.bodyAll})
	}
	if wanted[logSourceWaypoint] && len(s.Waypoint) > 0 {
		out = append(out, storage.LogSource{Services: s.Waypoint, BodyAll: s.bodyAll})
	}
	return out
}

// workloadLogSources resolves what to ask the store for one workload. The
// precise path needs the snapshot: mesh-config on, the cluster read, pods
// readable and not cut, and the workload in it. Every rung short of that
// falls back to matching by name AND namespace, and says which rung.
func (a *API) workloadLogSources(r *http.Request, namespace, name, wantWaypoint string) workloadLogSources {
	desc := workloadLogSources{meshLogSourcesDTO: meshLogSourcesDTO{
		App:      []string{name, name + "." + namespace},
		Ztunnel:  ztunnelServiceNames,
		Waypoint: []string{},
		Needles:  []string{},
	}}
	host := name + "." + namespace + ".svc"
	fallback := func(why string) workloadLogSources {
		desc.Fallback = why
		desc.bodyAll = [][]string{
			{host, `workload="` + name + `-`},
			{`namespace="` + namespace + `"`, host},
		}
		desc.Needles = append(desc.Needles, host)
		if ns, wp, ok := strings.Cut(wantWaypoint, "/"); ok && ns != "" && wp != "" {
			desc.Waypoint = []string{wp, wp + "." + ns}
		}
		return desc
	}

	if !a.modules.Enabled(modules.MeshConfig) {
		return fallback("pods are matched by name: mesh-config is off, so the workload's pods are not known")
	}
	snap := a.meshConfig().Snapshot(r.Context())
	switch {
	case snap.State != meshconfig.StateOK:
		return fallback("pods are matched by name: the cluster was not read — " + snap.Reason)
	case !podsReadable(snap):
		return fallback("pods are matched by name: " + checksSkipped(snap))
	case snap.PodsTruncated:
		return fallback("pods are matched by name: the pod list was cut, and this workload's may be past the cut")
	}
	needles := []string{host}
	var wl meshconfig.Workload
	if i := slices.IndexFunc(snap.Workloads, func(wl meshconfig.Workload) bool {
		return wl.Namespace == namespace && wl.Name == name
	}); i >= 0 {
		wl = snap.Workloads[i]
		for _, p := range snap.Pods {
			if podBelongsTo(p, wl) {
				needles = append(needles, p.Name)
			}
		}
	}
	// A workload with no pod in the snapshot cannot be told apart from its
	// namesake by pod name; the row may be stale or the join may have missed.
	if len(needles) == 1 {
		return fallback(fmt.Sprintf("pods are matched by name: no pod of %s/%s is in the cluster snapshot", namespace, name))
	}
	if len(needles) > maxLogNeedles {
		return fallback(fmt.Sprintf("pods are matched by name: %d pods is past the %d the store is asked for", len(needles)-1, maxLogNeedles))
	}
	desc.Precise = true
	desc.Needles = needles
	desc.bodyAll = [][]string{needles}
	if wl.Waypoint != "" {
		desc.Waypoint = []string{wl.Waypoint, wl.Waypoint + "." + wl.WaypointNamespace}
	}
	return desc
}
