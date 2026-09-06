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
	KindTelemetry           = "Telemetry"
	KindWasmPlugin          = "WasmPlugin"
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
	meshWide := ""
	byNamespace := map[string]string{}
	for _, pa := range peerAuths {
		mode := peerAuthMode(pa)
		if mode == "" {
			continue
		}
		// A PeerAuthentication with a selector targets specific workloads, not
		// the namespace. Attributing it to the namespace would report a mode
		// most of the namespace does not have.
		if hasSelector(pa) {
			continue
		}
		if pa.Namespace == rootNamespace {
			meshWide = mode
			continue
		}
		byNamespace[pa.Namespace] = mode
	}

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
		if mode, ok := byNamespace[ns.Name]; ok {
			row.MTLSMode = mode
		} else {
			// Empty when nothing applies: the mesh default governs, and naming
			// a mode we did not read would be a guess.
			row.MTLSMode = meshWide
		}
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

// hasSelector reports whether a policy targets specific workloads rather than
// everything in its scope.
func hasSelector(o Object) bool {
	sel, ok := o.Spec["selector"].(map[string]any)
	if !ok {
		return false
	}
	labels, ok := sel["matchLabels"].(map[string]any)
	return ok && len(labels) > 0
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
