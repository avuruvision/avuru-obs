package meshconfig

// Severity ranks a finding by what it costs, not by how unusual it is.
type Severity string

const (
	// SeverityError: this configuration does not work. Traffic it should carry
	// is not being carried.
	SeverityError Severity = "error"
	// SeverityWarning: it works and something about it is likely unintended.
	SeverityWarning Severity = "warning"
	// SeverityInfo: worth knowing, costs nothing.
	SeverityInfo Severity = "info"
)

// Code identifies a check. Ours, deliberately: the codes are part of the
// product's own vocabulary and appear in the UI, so they cannot be borrowed
// from another tool's numbering.
type Code string

const (
	// CodeRouteBackendMissing — a route points at a Service that is not there,
	// or a port it does not expose. The route attaches, matches, and drops
	// every request: the failure produces no traffic to observe, which is
	// exactly why it needs reading rather than measuring.
	CodeRouteBackendMissing Code = "MESH_ROUTE_BACKEND_MISSING"
	// CodeRouteParentMissing — a parentRef naming a Gateway that does not
	// exist. The route is inert; nothing serves it.
	CodeRouteParentMissing Code = "MESH_ROUTE_PARENT_MISSING"
	// CodeGatewayNoRoutes — a listener nothing attaches to. Usually a
	// half-finished migration; occasionally the reason an endpoint 404s.
	CodeGatewayNoRoutes Code = "MESH_GATEWAY_NO_ROUTES"
	// CodeHostUnresolved — a VirtualService or DestinationRule host matching no
	// Service or ServiceEntry. Most often a typo or a deleted service.
	CodeHostUnresolved Code = "MESH_HOST_UNRESOLVED"
	// CodeWaypointMissing — a namespace, service or workload bound to a
	// waypoint that is not deployed. In ambient this silently removes the L7
	// path: every policy and route meant for the waypoint is skipped.
	CodeWaypointMissing Code = "MESH_WAYPOINT_MISSING"
	// CodeAmbientNotEnrolled — a workload labelled for ambient, running, and
	// neither captured by the node agent nor injected. The single most common
	// ambient misconfiguration, and invisible to traffic alone. Judged from
	// pods, so it goes silent — and says so — when the pod list was not
	// readable or was cut.
	CodeAmbientNotEnrolled Code = "MESH_AMBIENT_NOT_ENROLLED"
	// CodeMTLSConflict — a DestinationRule disabling TLS toward a workload
	// whose effective PeerAuthentication is STRICT, or demanding mutual TLS of
	// one that disables it. The two disagree and the connection fails.
	CodeMTLSConflict Code = "MESH_MTLS_CONFLICT"
	// CodePolicyNoMatch — a policy whose selector matches no pod in its
	// namespace, or whose targetRef names an object that does not exist. A
	// policy protecting nothing reads as protection. The selector half is
	// judged from pods and goes silent when they were not readable or cut.
	CodePolicyNoMatch Code = "MESH_POLICY_NO_MATCH"
	// CodeL7WithoutWaypoint — an object that only a waypoint can honour —
	// HTTP-level authorization, request authentication, HTTP routing, HTTP
	// connection pools — in an ambient namespace where its targets have none.
	// The L4 data plane cannot evaluate it: HTTP allow rules fail closed and
	// routing rules are not applied.
	CodeL7WithoutWaypoint Code = "MESH_L7_WITHOUT_WAYPOINT"
	// CodeDataplaneConflict — a workload asked to be two things at once: a
	// sidecar in an ambient namespace, a pod labelled for both modes, or a
	// waypoint binding on a workload that is not ambient.
	CodeDataplaneConflict Code = "MESH_DATAPLANE_CONFLICT"
	// CodeSubsetMissing — a VirtualService route to a subset no
	// DestinationRule for that host defines. The route matches and has no
	// upstream.
	CodeSubsetMissing Code = "MESH_SUBSET_MISSING"
	// CodeHostConflict — two mesh-bound VirtualServices for one host, or two
	// DestinationRules claiming one host. Only one is applied; the other looks
	// configured and does nothing.
	CodeHostConflict Code = "MESH_HOST_CONFLICT"
	// CodeGatewayNoWorkload — a Gateway, waypoints included, that no running
	// pod serves. Its listeners exist and nothing answers on them.
	CodeGatewayNoWorkload Code = "MESH_GATEWAY_NO_WORKLOAD"
	// CodeListenerConflict — listeners on one Gateway that share a port and
	// hostname with different protocols, or share a name. The gateway is not
	// programmed while they conflict.
	CodeListenerConflict Code = "MESH_LISTENER_CONFLICT"
	// CodePrincipalUnknown — an authorization rule naming a service account
	// no running workload uses. An ALLOW for it allows nobody; a DENY denies
	// nobody.
	CodePrincipalUnknown Code = "MESH_PRINCIPAL_UNKNOWN"
)

// What a finding's Ref names, so a screen can link it to the right list
// rather than guess from its shape.
const (
	RefKindService         = "Service"
	RefKindGateway         = "Gateway"
	RefKindWorkload        = "Workload"
	RefKindNamespace       = "Namespace"
	RefKindServiceAccount  = "ServiceAccount"
	RefKindVirtualService  = "VirtualService"
	RefKindDestinationRule = "DestinationRule"
	RefKindHost            = "Host"
	RefKindWaypoint        = "Waypoint"
)

// Finding is one problem with one object.
//
// Message says what is wrong; Hint says what to do. They are separate fields
// because a finding that only states the problem sends the reader looking, and
// this product's discipline everywhere else is to name the fix.
type Finding struct {
	Code     Code
	Severity Severity
	Message  string
	Hint     string
	// Ref is the object the finding is ABOUT when that differs from the object
	// it was found on — a route's missing backend names the Service, so the
	// reader can search for the thing that is absent.
	Ref string
	// RefKind says what Ref names — one of the RefKind constants — so the
	// screen can open it: a Service and a Workload are both spelled
	// "namespace/name" and live on different tabs.
	RefKind string
}
