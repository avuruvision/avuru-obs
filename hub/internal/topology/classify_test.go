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

// Label evidence is the mesh's own word about its own workload, and it has to
// beat a name list that was never going to guess "public-edge".
func TestEvidencePromotesAnUnrecognisedName(t *testing.T) {
	c := New(Default())
	if c.IsTransport("public-edge") {
		t.Fatal("precondition: public-edge should match no built-in pattern")
	}
	withEv := c.WithEvidence([]string{"public-edge"})
	if !withEv.IsTransport("public-edge") {
		t.Error("a workload the mesh labelled as its own data plane is still drawn as an application")
	}
	if withEv.Role("public-edge") != RoleTransport {
		t.Error("Role disagrees with IsTransport")
	}
}

// The operator's explicit list is an override, and an override a signal can
// defeat is not an override.
func TestApplicationsBeatEvidence(t *testing.T) {
	c := New(Config{Applications: []string{"payments-gateway"}}).
		WithEvidence([]string{"payments-gateway"})
	if c.IsTransport("payments-gateway") {
		t.Error("label evidence overrode the operator's applications list")
	}
}

// Absence of a label is not evidence of absence: a sidecar is a container
// inside the application's pod and wears the application's labels, so there is
// nothing to read. Evidence must never demote.
func TestEvidenceNeverDemotes(t *testing.T) {
	// istio-proxy is a built-in, and no label will ever name it.
	c := New(Default()).WithEvidence([]string{"public-edge"})
	if !c.IsTransport("istio-proxy") {
		t.Error("a sidecar fell off the transport list because no label mentioned it")
	}
}

// An install where nothing is labelled must classify exactly as it did before
// this existed — the rider is strictly additive or it is a regression.
func TestEmptyEvidenceChangesNothing(t *testing.T) {
	base := New(Default())
	for _, name := range []string{
		"istio-proxy", "ztunnel", "global-waypoint.istio-waypoint",
		"checkout", "payments", "gateway-service", "api-proxy-cache",
	} {
		if got, want := base.WithEvidence(nil).IsTransport(name), base.IsTransport(name); got != want {
			t.Errorf("%s: with empty evidence = %v, want %v", name, got, want)
		}
		if got, want := base.WithEvidence([]string{}).IsTransport(name), base.IsTransport(name); got != want {
			t.Errorf("%s: with zero-length evidence = %v, want %v", name, got, want)
		}
	}
}

// Evidence arrives from telemetry, where a name's case is whatever the cluster
// wrote — the classifier is case-insensitive everywhere else and must be here.
func TestEvidenceIsCaseInsensitive(t *testing.T) {
	c := New(Default()).WithEvidence([]string{"Public-Edge"})
	if !c.IsTransport("public-edge") {
		t.Error("evidence did not match a differently-cased name")
	}
}
