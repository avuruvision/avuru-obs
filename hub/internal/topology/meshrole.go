package topology

import "strings"

// MeshRole is what KIND of transport a workload is: the finer question the mesh
// screen asks about a workload the map only needs to know is not an application.
//
// Deliberately NOT folded into Role. Role has exactly two members because the
// map draws exactly two shapes, and every consumer of it — the map, the MCP
// context, hop collapse — asks only "is this a dependency or a hop". Widening
// that enum to six would make all of them learn a vocabulary they never use, to
// answer a question only one screen asks.
type MeshRole string

const (
	// MeshRoleUnknown is transport we cannot name any more precisely. It
	// renders blank rather than guessing: a proxy filed under the wrong role
	// is worse than a proxy filed under none, because the wrong one is
	// believed.
	MeshRoleUnknown MeshRole = ""
	// MeshRoleControlPlane programs the data plane and carries no user traffic.
	MeshRoleControlPlane MeshRole = "control-plane"
	// MeshRoleIngressGateway is traffic entering the mesh from outside.
	MeshRoleIngressGateway MeshRole = "ingress-gateway"
	// MeshRoleEgressGateway is traffic leaving the mesh.
	MeshRoleEgressGateway MeshRole = "egress-gateway"
	// MeshRoleGateway is a gateway whose direction we cannot establish. The
	// Gateway API label that identifies it is worn by ingress gateways and
	// ambient waypoints alike, so claiming a direction from it would be a
	// coin toss dressed as a fact.
	MeshRoleGateway MeshRole = "gateway"
	// MeshRoleWaypoint is an ambient L7 proxy: per-namespace or per-service,
	// off the pod, and only in the path of traffic explicitly routed to it.
	MeshRoleWaypoint MeshRole = "waypoint"
	// MeshRoleZtunnel is the ambient L4 data plane: one per node, in the path
	// of everything its node's enrolled pods send or receive.
	MeshRoleZtunnel MeshRole = "ztunnel"
	// MeshRoleSidecar is a proxy sharing a pod with the application it fronts.
	MeshRoleSidecar MeshRole = "sidecar"
)

// Mesh label keys the hub interprets. The chart owns WHICH labels are collected
// (sensor-config.yaml, curated under the avuru.transport. prefix); this list is
// only what we know how to read. A key the chart adds and this list does not
// name is ignored, never an error — the two must be free to move separately, or
// the coupling the prefix exists to avoid comes straight back.
const (
	meshLabelIstioComponent   = "avuru.transport.istio_component"
	meshLabelLinkerdComponent = "avuru.transport.linkerd_component"
	meshLabelGateway          = "avuru.transport.gateway"
	meshLabelIstioGateway     = "avuru.transport.istio_gateway"
)

// meshRolePatterns maps workload names to roles, most specific first.
//
// These are a SUBSET of builtinTransport, never a superset: a name that reaches
// here has already been classified as transport, and this list only says which
// kind. Order matters — "*-waypoint" must be tried before any gateway pattern,
// because an ambient waypoint is a Gateway resource and would otherwise be
// filed as an ingress gateway it has nothing in common with.
var meshRolePatterns = []struct {
	role     MeshRole
	patterns []string
}{
	{MeshRoleZtunnel, []string{"ztunnel", "ztunnel-*"}},
	{MeshRoleWaypoint, []string{"waypoint", "*-waypoint", "waypoint-*"}},
	{MeshRoleIngressGateway, []string{"istio-ingressgateway*"}},
	{MeshRoleEgressGateway, []string{"istio-egressgateway*"}},
	{MeshRoleControlPlane, []string{
		"istiod",
		"linkerd-destination", "linkerd-identity", "linkerd-proxy-injector",
		"kuma-control-plane",
	}},
	{MeshRoleSidecar, []string{
		"istio-proxy",
		"linkerd-proxy",
		"consul-dataplane*", "consul-connect-envoy*",
		"kuma-dp", "kuma-sidecar",
	}},
}

// MeshRole names the kind of transport `name` is, using the labels the mesh
// wrote on its own workload to refine what the name alone cannot settle.
//
// Names decide, labels refine — the opposite order to WithEvidence, and for a
// different reason. There, a label is the mesh identifying itself as transport
// at all, which no glob may override. Here, both inputs agree the workload IS
// transport and the question is which kind: the name distinguishes a waypoint
// from an ingress gateway, and the Gateway API label cannot, because both wear
// it. So the sharper signal goes first and the label answers only what the name
// left open.
//
// An application is never given a role, so an operator who rescues a workload
// through the `applications` override gets it back with no mesh identity at all.
func (c Classifier) MeshRole(name string, labels map[string]string) MeshRole {
	if !c.IsTransport(name) {
		return MeshRoleUnknown
	}
	for _, r := range meshRolePatterns {
		if matchAny(r.patterns, name) {
			return r.role
		}
	}
	return meshRoleFromLabels(labels)
}

// meshRoleFromLabels reads the component labels a mesh writes on itself.
func meshRoleFromLabels(labels map[string]string) MeshRole {
	if len(labels) == 0 {
		return MeshRoleUnknown
	}
	// operator.istio.io/component is the one label that states the job rather
	// than the name, so it is the only one allowed to claim a direction.
	switch strings.ToLower(strings.TrimSpace(labels[meshLabelIstioComponent])) {
	case "pilot", "istiod":
		return MeshRoleControlPlane
	case "ingressgateways", "ingressgateway":
		return MeshRoleIngressGateway
	case "egressgateways", "egressgateway":
		return MeshRoleEgressGateway
	}
	// linkerd.io/control-plane-component says so in the key itself; any value
	// is a control-plane component.
	if strings.TrimSpace(labels[meshLabelLinkerdComponent]) != "" {
		return MeshRoleControlPlane
	}
	if strings.TrimSpace(labels[meshLabelGateway]) != "" ||
		strings.TrimSpace(labels[meshLabelIstioGateway]) != "" {
		return MeshRoleGateway
	}
	return MeshRoleUnknown
}
