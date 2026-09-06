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
	// CodeWaypointMissing — a workload or namespace routed to a waypoint that
	// is not deployed. In ambient this silently removes the L7 path.
	CodeWaypointMissing Code = "MESH_WAYPOINT_MISSING"
	// CodeAmbientNotEnrolled — a namespace labelled for ambient whose workloads
	// never appear behind ztunnel. The single most common ambient
	// misconfiguration, and invisible to traffic alone.
	CodeAmbientNotEnrolled Code = "MESH_AMBIENT_NOT_ENROLLED"
	// CodeMTLSConflict — a DestinationRule disabling TLS underneath a strict
	// PeerAuthentication. The two disagree and the connection fails.
	CodeMTLSConflict Code = "MESH_MTLS_CONFLICT"
	// CodePolicyNoMatch — an AuthorizationPolicy or Sidecar whose selector
	// matches no workload. A policy protecting nothing reads as protection.
	CodePolicyNoMatch Code = "MESH_POLICY_NO_MATCH"
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
}
