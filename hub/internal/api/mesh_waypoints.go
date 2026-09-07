package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
)

// labelGatewayName is the label the Gateway API's implementation stamps on
// the pods that serve a Gateway, and so on a waypoint's own pods.
const labelGatewayName = "gateway.networking.k8s.io/gateway-name"

// meshWaypointDTO is one waypoint: what it is, and whether it is there.
type meshWaypointDTO struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Scope is what the waypoint serves — "service", "workload", "all" or
	// "none" — from its Gateway object. Absent when no Gateway of that name
	// was read: something is bound to a waypoint that is not deployed, which
	// is the MESH_WAYPOINT_MISSING case and not a scope.
	Scope string `json:"scope,omitempty"`
	// Running says a Running pod serves the Gateway. A pointer, because "not
	// running" and "could not look at pods" are different instructions, and
	// only the first is a false.
	Running *bool `json:"running,omitempty"`
}

type meshWaypointResponse struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Waypoint is absent when the cluster could not be read.
	Waypoint *meshWaypointDTO `json:"waypoint,omitempty"`
	// What is bound to it, at each level the mesh allows a binding: whole
	// namespaces, single Services, single workloads. Each as "namespace/name"
	// except namespaces, which are their own name.
	Namespaces []string `json:"namespaces"`
	Services   []string `json:"services"`
	Workloads  []string `json:"workloads"`
}

// handleMeshWaypoint answers "what does this waypoint serve" from the
// inventory's bindings — the question a waypoint's own traffic cannot answer,
// because a waypoint nothing is bound to and a waypoint whose clients are idle
// look the same on the wire.
func (a *API) handleMeshWaypoint(w http.ResponseWriter, r *http.Request) error {
	namespace, name := r.PathValue("namespace"), r.PathValue("name")
	snap := a.meshConfig().Snapshot(r.Context())
	resp := meshWaypointResponse{
		State:      string(snap.State),
		Reason:     snap.Reason,
		Namespaces: []string{},
		Services:   []string{},
		Workloads:  []string{},
	}
	if snap.State != meshconfig.StateOK {
		writeJSON(w, http.StatusOK, resp)
		return nil
	}

	var gateway *meshconfig.Object
	for i := range snap.Objects {
		o := &snap.Objects[i]
		if o.Kind == meshconfig.KindGateway && o.Namespace == namespace && o.Name == name {
			gateway = o
			break
		}
	}
	for _, ns := range snap.Namespaces {
		if boundTo(ns.Waypoint, ns.WaypointNamespace, ns.Name, namespace, name) {
			resp.Namespaces = append(resp.Namespaces, ns.Name)
		}
	}
	for _, s := range snap.Services {
		if boundTo(s.Waypoint, s.WaypointNamespace, s.Namespace, namespace, name) {
			resp.Services = append(resp.Services, s.Namespace+"/"+s.Name)
		}
	}
	for _, wl := range snap.Workloads {
		if boundTo(wl.Waypoint, wl.WaypointNamespace, wl.Namespace, namespace, name) {
			resp.Workloads = append(resp.Workloads, wl.Namespace+"/"+wl.Name)
		}
	}
	if gateway == nil && len(resp.Namespaces)+len(resp.Services)+len(resp.Workloads) == 0 {
		return notFound("no waypoint %s/%s in the cluster snapshot: no Gateway of that name, and nothing bound to it", namespace, name)
	}

	wp := &meshWaypointDTO{Namespace: namespace, Name: name}
	if gateway != nil {
		wp.Scope = meshconfig.WaypointScope(*gateway)
	}
	if podsReadable(snap) {
		running := waypointRunning(snap.Pods, namespace, name)
		wp.Running = &running
	}
	resp.Waypoint = wp
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// boundTo reports whether a binding (waypoint, waypointNS) on an object in
// ownNS names the waypoint asked for. An empty waypoint namespace means the
// object's own, as the mesh reads it.
func boundTo(waypoint, waypointNS, ownNS, namespace, name string) bool {
	if waypoint != name {
		return false
	}
	if waypointNS == "" {
		waypointNS = ownNS
	}
	return waypointNS == namespace
}

// waypointRunning reports whether any Running pod serves the Gateway, by the
// label its implementation stamps on them.
func waypointRunning(pods []meshconfig.Pod, namespace, name string) bool {
	for _, p := range pods {
		if p.Namespace == namespace && p.Phase == podPhaseRunning && p.Labels[labelGatewayName] == name {
			return true
		}
	}
	return false
}
