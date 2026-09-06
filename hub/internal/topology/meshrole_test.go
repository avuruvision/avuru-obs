package topology

import "testing"

// The names are the ones an Istio ambient install actually produces, in the
// "workload.namespace" shape OBI's k8s labels give them. Getting these right
// from the name alone is what lets the mesh screen be useful before anyone
// configures anything.
func TestMeshRoleFromNames(t *testing.T) {
	c := New(Default())

	for name, want := range map[string]MeshRole{
		"ztunnel":                               MeshRoleZtunnel,
		"ztunnel.istio-system":                  MeshRoleZtunnel,
		"ztunnel-ambient":                       MeshRoleZtunnel,
		"global-waypoint.istio-waypoint":        MeshRoleWaypoint,
		"reviews-waypoint":                      MeshRoleWaypoint,
		"waypoint":                              MeshRoleWaypoint,
		"istio-ingressgateway-istio.istio-edge": MeshRoleIngressGateway,
		"istio-ingressgateway":                  MeshRoleIngressGateway,
		"istio-egressgateway":                   MeshRoleEgressGateway,
		"istiod":                                MeshRoleControlPlane,
		"istiod.istio-system":                   MeshRoleControlPlane,
		"istio-proxy":                           MeshRoleSidecar,
		"ISTIO-PROXY":                           MeshRoleSidecar,
		"linkerd-proxy":                         MeshRoleSidecar,
		"linkerd-destination":                   MeshRoleControlPlane,
		"consul-dataplane-web":                  MeshRoleSidecar,
		"kuma-dp":                               MeshRoleSidecar,
		"kuma-control-plane":                    MeshRoleControlPlane,
	} {
		if got := c.MeshRole(name, nil); got != want {
			t.Errorf("MeshRole(%q) = %q, want %q", name, got, want)
		}
	}
}

// A waypoint IS a Gateway resource and wears the Gateway API label. If the
// label were allowed to answer first, every ambient waypoint in the cluster
// would be filed as an ingress gateway.
func TestMeshRoleNameBeatsGatewayLabel(t *testing.T) {
	c := New(Default())
	labels := map[string]string{meshLabelGateway: "global-waypoint"}

	if got := c.MeshRole("global-waypoint.istio-waypoint", labels); got != MeshRoleWaypoint {
		t.Errorf("MeshRole = %q, want waypoint", got)
	}
}

// Labels answer only what the name left open — the case an install hits when
// it names its gateway after what it does rather than after the product.
func TestMeshRoleFromLabels(t *testing.T) {
	c := New(Config{Transport: []string{"edge-*"}})

	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   MeshRole
	}{
		{"edge-front", map[string]string{meshLabelIstioComponent: "IngressGateways"}, MeshRoleIngressGateway},
		{"edge-front", map[string]string{meshLabelIstioComponent: "ingressgateway"}, MeshRoleIngressGateway},
		{"edge-out", map[string]string{meshLabelIstioComponent: "EgressGateways"}, MeshRoleEgressGateway},
		{"edge-brain", map[string]string{meshLabelIstioComponent: "Pilot"}, MeshRoleControlPlane},
		{"edge-lnkd", map[string]string{meshLabelLinkerdComponent: "destination"}, MeshRoleControlPlane},
		// The Gateway API label alone cannot tell ingress from waypoint, so it
		// must not pretend to.
		{"edge-any", map[string]string{meshLabelGateway: "public"}, MeshRoleGateway},
		{"edge-any", map[string]string{meshLabelIstioGateway: "public"}, MeshRoleGateway},
		// An unknown component value falls through rather than guessing.
		{"edge-weird", map[string]string{meshLabelIstioComponent: "Nonesuch"}, MeshRoleUnknown},
		// A label the chart collects and the hub does not read is ignored.
		{"edge-plain", map[string]string{"avuru.transport.future": "x"}, MeshRoleUnknown},
		{"edge-plain", nil, MeshRoleUnknown},
	} {
		if got := c.MeshRole(tc.name, tc.labels); got != tc.want {
			t.Errorf("MeshRole(%q, %v) = %q, want %q", tc.name, tc.labels, got, tc.want)
		}
	}
}

// An application has no mesh identity, including one the operator rescued from
// the built-in transport list. Anything else would put it back on the mesh
// screen it was explicitly taken off.
func TestMeshRoleIgnoresApplications(t *testing.T) {
	c := New(Config{Applications: []string{"waypoint", "istiod"}})

	for _, name := range []string{"waypoint", "istiod", "payment-gateway", ""} {
		if got := c.MeshRole(name, map[string]string{meshLabelIstioComponent: "Pilot"}); got != MeshRoleUnknown {
			t.Errorf("MeshRole(%q) = %q, want unknown", name, got)
		}
	}
}
