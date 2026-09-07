package meshconfig

import "strings"

// Kubernetes and Istio label keys this package reads. Named here rather than
// inline so the vocabulary of "how a mesh says what it is" lives in one place.
const (
	labelDataplaneMode = "istio.io/dataplane-mode"
	labelInjection     = "istio-injection"
	labelRevision      = "istio.io/rev"
	labelUseWaypoint   = "istio.io/use-waypoint"
	labelWaypointNS    = "istio.io/use-waypoint-namespace"
	labelWaypointForNS = "istio.io/waypoint-for"
	valueAmbient       = "ambient"
	valueInjectEnabled = "enabled"
	valueWaypointNone  = "none"
	// DataplaneAmbient and DataplaneSidecar are the two ways into a mesh.
	DataplaneAmbient = "ambient"
	DataplaneSidecar = "sidecar"
)

// Kinds this package reads, as they appear on Object.Kind.
const (
	KindNamespace           = "Namespace"
	KindPod                 = "Pod"
	KindService             = "Service"
	KindGateway             = "Gateway"
	KindHTTPRoute           = "HTTPRoute"
	KindGRPCRoute           = "GRPCRoute"
	KindVirtualService      = "VirtualService"
	KindDestinationRule     = "DestinationRule"
	KindServiceEntry        = "ServiceEntry"
	KindSidecar             = "Sidecar"
	KindPeerAuthentication  = "PeerAuthentication"
	KindAuthorizationPolicy = "AuthorizationPolicy"
	// KindRequestAuthentication is not watched yet; the validator knows the
	// kind so that the day it is, the L7 check needs no change.
	KindRequestAuthentication = "RequestAuthentication"
	KindTelemetry             = "Telemetry"
	KindWasmPlugin            = "WasmPlugin"
	KindDeployment            = "Deployment"
	KindDaemonSet             = "DaemonSet"
	KindStatefulSet           = "StatefulSet"
	// KindReplicaSet is never watched; it is what a pod names as its owner
	// when its Deployment could not be confirmed.
	KindReplicaSet = "ReplicaSet"
)

// NamespacesFrom turns raw namespace objects into mesh membership rows.
//
// Pure, and separated from the informer for the usual reason: this is the part
// with judgement in it. Whether a namespace is in the mesh is a question about
// LABELS, which is why this row can exist for a namespace that has never sent a
// byte of telemetry — the entire point of reading configuration at all.
//
// rootNamespace is where a mesh-wide PeerAuthentication lives (istio-system on
// a default install). A mesh-wide policy is the fallback for every namespace
// that does not set its own.
func NamespacesFrom(namespaces, peerAuths []Object, rootNamespace string) []Namespace {
	out := make([]Namespace, 0, len(namespaces))
	for _, ns := range namespaces {
		row := Namespace{
			Name:          ns.Name,
			Labels:        ns.Labels,
			DataplaneMode: dataplaneMode(ns.Labels),
		}
		// A waypoint explicitly set to "none" is an opt-OUT, and reporting the
		// literal "none" as a waypoint name would be worse than reporting
		// nothing.
		if wp := ns.Labels[labelUseWaypoint]; wp != "" && wp != valueWaypointNone {
			row.Waypoint = wp
			row.WaypointNamespace = ns.Labels[labelWaypointNS]
			if row.WaypointNamespace == "" {
				row.WaypointNamespace = ns.Name
			}
		}
		// The same resolver the workloads use, asked with no labels: only
		// scope-wide policies can answer for a namespace, and a selector
		// policy attributed here would report a mode most of the namespace
		// does not have.
		mtls := DeclaredMTLSFor(nil, ns.Name, peerAuths, rootNamespace)
		row.MTLSMode, row.MTLSSource, row.MTLSPolicy = mtls.Mode, mtls.Source, mtls.Policy
		out = append(out, row)
	}
	return out
}

// dataplaneMode reads how a namespace joins the mesh, if it does.
//
// Ambient is checked first because a namespace carrying both labels is being
// migrated, and the ambient label is the one that takes effect.
func dataplaneMode(labels map[string]string) string {
	switch {
	case strings.EqualFold(labels[labelDataplaneMode], valueAmbient):
		return DataplaneAmbient
	case strings.EqualFold(labels[labelInjection], valueInjectEnabled):
		return DataplaneSidecar
	case labels[labelRevision] != "":
		// Revision-based injection: the value names the control-plane revision
		// rather than saying "enabled", so presence is the signal.
		return DataplaneSidecar
	default:
		return ""
	}
}

// peerAuthMode digs spec.mtls.mode out of a PeerAuthentication.
func peerAuthMode(o Object) string {
	mtls, ok := o.Spec["mtls"].(map[string]any)
	if !ok {
		return ""
	}
	mode, _ := mtls["mode"].(string)
	return mode
}

// IsWaypoint reports whether a Gateway object is an ambient waypoint rather
// than an ingress gateway.
//
// The gatewayClassName is what separates them, and it is the reason the
// telemetry-side classifier cannot: both wear the same Gateway API label, and
// only the object says which class it belongs to.
func IsWaypoint(o Object) bool {
	if o.Kind != KindGateway {
		return false
	}
	class, _ := o.Spec["gatewayClassName"].(string)
	return strings.Contains(strings.ToLower(class), "waypoint")
}

// WaypointScope reports what a waypoint serves — "service", "workload", "all"
// or "none" — from istio.io/waypoint-for, defaulting to service as Istio does.
func WaypointScope(o Object) string {
	if v := o.Labels[labelWaypointForNS]; v != "" {
		return v
	}
	return "service"
}
