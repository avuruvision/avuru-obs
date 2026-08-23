package topology

import "testing"

// The names in the "transport" block are real ones observed on an Istio
// ambient install whose map drew mesh hops as application dependencies. The
// "service" block is the half that matters more: these MUST survive, because a
// false positive deletes a real service from the map.
func TestClassifyDefaults(t *testing.T) {
	c := New(Default())

	transport := []string{
		"global-waypoint.istio-waypoint",
		"istio-ingressgateway-istio.istio-edge",
		"istio-proxy",
		"ISTIO-PROXY",
		"ztunnel",
		"ztunnel-ambient",
		"waypoint",
		"reviews-waypoint",
		"linkerd-destination",
		"linkerd-proxy",
		"consul-dataplane-web",
		"kuma-dp",
		"checkout.istio-system",
	}
	for _, name := range transport {
		if got := c.Role(name); got != RoleTransport {
			t.Errorf("Role(%q) = %q, want transport", name, got)
		}
	}

	services := []string{
		"payment-gateway",
		"api-gateway",
		"apisix-gateway",
		"auth-service",
		"envoy-fleet-manager",
		"proxy-cache",
		"my-istio-dashboard",
		"waypoints-api",
		"checkout.default",
		"",
	}
	for _, name := range services {
		if got := c.Role(name); got != RoleService {
			t.Errorf("Role(%q) = %q, want service", name, got)
		}
	}
}

// The applications list is the rescue hatch: it must beat both the built-ins
// and an operator's own transport addition.
func TestApplicationsOverrideTransport(t *testing.T) {
	c := New(Config{
		Transport:    []string{"edge-*"},
		Applications: []string{"waypoint", "edge-router"},
	})
	for name, want := range map[string]Role{
		"waypoint":         RoleService,
		"edge-router":      RoleService,
		"edge-shipper":     RoleTransport,
		"reviews-waypoint": RoleTransport,
	} {
		if got := c.Role(name); got != want {
			t.Errorf("Role(%q) = %q, want %q", name, got, want)
		}
	}
}

// DisableDefaults means exactly that — the built-ins stop applying, so an
// install whose naming collides with them can start from nothing.
func TestDisableDefaults(t *testing.T) {
	c := New(Config{DisableDefaults: true, Transport: []string{"mesh-*"}})
	if got := c.Role("istio-proxy"); got != RoleService {
		t.Errorf("Role(istio-proxy) with defaults disabled = %q, want service", got)
	}
	if got := c.Role("mesh-relay"); got != RoleTransport {
		t.Errorf("Role(mesh-relay) = %q, want transport", got)
	}
}

// A pattern must not match across a dot by accident: segments are matched
// individually, and "*" in path.Match does not cross the separator we split on
// ourselves, so "istio-*" must not swallow "istio-shop.payments".
func TestSegmentMatchingIsPerSegment(t *testing.T) {
	c := New(Config{DisableDefaults: true, Transport: []string{"istio-system"}})
	if got := c.Role("checkout.istio-system"); got != RoleTransport {
		t.Errorf("namespace segment should match: got %q", got)
	}
	if got := c.Role("istio-system-clone.default"); got != RoleService {
		t.Errorf("partial segment must not match: got %q", got)
	}
}
