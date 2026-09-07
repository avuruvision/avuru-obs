package meshconfig

import (
	"strings"
	"testing"
)

var ambientNS = Namespace{Name: "amb", DataplaneMode: DataplaneAmbient}

// The most common ambient misconfiguration: labelled, running, captured by
// nobody — and producing exactly the traffic it produced before the label.
func TestAmbientNotEnrolled(t *testing.T) {
	hostNet := running("amb", "agent-0", KindDaemonSet, "agent", nil)
	hostNet.HostNetwork = true
	optedOut := running("amb", "legacy-0", KindStatefulSet, "legacy", map[string]string{labelDataplaneMode: "none"})
	pending := running("amb", "new-0", KindStatefulSet, "new", nil)
	pending.Phase = "Pending"
	pods := []Pod{
		running("amb", "cart-0", KindStatefulSet, "cart", nil),
		captured(running("amb", "pay-0", KindStatefulSet, "pay", nil)),
		withSidecar(running("amb", "old-0", KindStatefulSet, "old", nil)),
		hostNet, optedOut, pending,
		// Not an ambient namespace: nothing was asked for.
		running("plain", "web-0", KindStatefulSet, "web", nil),
	}
	snap := judge(Snapshot{Namespaces: []Namespace{ambientNS, {Name: "plain"}}, Pods: pods})

	for id, want := range map[string]bool{
		"amb/cart": true, "amb/pay": false, "amb/old": false, "amb/agent": false,
		"amb/legacy": false, "amb/new": false, "plain/web": false,
	} {
		if got := hasCode(workloadFindings(snap, id), CodeAmbientNotEnrolled); got != want {
			t.Errorf("%s not-enrolled = %v, want %v", id, got, want)
		}
	}
	// The sidecar in the ambient namespace is a conflict, not a missed
	// enrolment: it is in the mesh, just not the way the namespace says.
	if !hasCode(workloadFindings(snap, "amb/old"), CodeDataplaneConflict) {
		t.Error("a sidecar in an ambient namespace was not reported as a data-plane conflict")
	}

	cut := judge(Snapshot{Namespaces: []Namespace{ambientNS}, Pods: pods, PodsTruncated: true})
	if got := codes(cut)[CodeAmbientNotEnrolled]; got != 0 || cut.ChecksSkipped == "" {
		t.Errorf("with pods cut: findings = %d, skipped = %q; want none and a sentence", got, cut.ChecksSkipped)
	}
}

// A workload asked to be two things at once, one finding per shape — and a
// clean ambient workload, bound to its waypoint, has none.
func TestDataplaneConflict(t *testing.T) {
	both := running("amb", "both-0", KindStatefulSet, "both",
		map[string]string{labelDataplaneMode: "ambient", labelSidecarInject: "true"})
	pods := []Pod{
		captured(withSidecar(running("amb", "twice-0", KindStatefulSet, "twice", nil))),
		withSidecar(running("amb", "sidecar-0", KindStatefulSet, "sidecar", nil)),
		captured(both),
		running("plain", "bound-0", KindStatefulSet, "bound", map[string]string{labelUseWaypoint: "wp"}),
		captured(running("amb", "clean-0", KindStatefulSet, "clean", map[string]string{labelUseWaypoint: "wp"})),
	}
	snap := judge(Snapshot{
		Namespaces: []Namespace{ambientNS, {Name: "plain"}},
		Objects:    []Object{obj(KindGateway, "amb", "wp", map[string]any{"gatewayClassName": "istio-waypoint"})},
		Pods:       pods,
	})
	for id, want := range map[string]string{
		"amb/twice":   "runs a sidecar and is captured",
		"amb/sidecar": "runs a sidecar in an ambient namespace",
		"amb/both":    "labelled for ambient and for sidecar injection",
		"plain/bound": "bound to waypoint plain/wp but not in ambient",
	} {
		var found bool
		for _, f := range workloadFindings(snap, id) {
			if f.Code == CodeDataplaneConflict && strings.Contains(f.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no conflict saying %q in %+v", id, want, workloadFindings(snap, id))
		}
	}
	if f := workloadFindings(snap, "amb/clean"); len(f) != 0 {
		t.Errorf("a clean ambient workload has findings: %+v", f)
	}
	// Captured AND injected is one finding, not that one plus the sidecar one.
	var n int
	for _, f := range workloadFindings(snap, "amb/twice") {
		if f.Code == CodeDataplaneConflict {
			n++
		}
	}
	if n != 1 {
		t.Errorf("amb/twice conflicts = %d, want 1", n)
	}
}

// A binding to a waypoint that is not deployed silently removes the L7 path.
// It is reported once, on the object whose own label made the binding.
func TestWaypointMissing(t *testing.T) {
	namespaces := []Namespace{
		{Name: "broken", DataplaneMode: DataplaneAmbient, Waypoint: "nonesuch", WaypointNamespace: "broken"},
		{Name: "shared", DataplaneMode: DataplaneAmbient, Waypoint: "global", WaypointNamespace: "istio-waypoint"},
		// A literal "none" is an opt-out, and NamespacesFrom leaves Waypoint
		// empty for it; the row below is what that produces.
		{Name: "opted-out", DataplaneMode: DataplaneAmbient},
	}
	objects := []Object{gateway("istio-waypoint", "global", "istio-waypoint", nil)}
	pods := []Pod{
		// Inherits the broken binding: the namespace's finding, not its own.
		running("broken", "inherits-0", KindStatefulSet, "inherits", nil),
		// Overrides it with a waypoint that exists: nothing to report.
		running("broken", "overrides-0", KindStatefulSet, "overrides",
			map[string]string{labelUseWaypoint: "global", labelWaypointNS: "istio-waypoint"}),
		// Its own binding, to nothing.
		running("shared", "own-0", KindStatefulSet, "own", map[string]string{labelUseWaypoint: "missing"}),
	}
	snap := judge(Snapshot{Namespaces: namespaces, Objects: objects, Pods: pods})

	for name, want := range map[string]bool{"broken": true, "shared": false, "opted-out": false} {
		if got := hasCode(namespaceFindings(snap, name), CodeWaypointMissing); got != want {
			t.Errorf("namespace %s waypoint-missing = %v, want %v", name, got, want)
		}
	}
	for id, want := range map[string]bool{"broken/inherits": false, "broken/overrides": false, "shared/own": true} {
		if got := hasCode(workloadFindings(snap, id), CodeWaypointMissing); got != want {
			t.Errorf("workload %s waypoint-missing = %v, want %v", id, got, want)
		}
	}
	f := namespaceFindings(snap, "broken")
	if len(f) != 1 || f[0].Ref != "broken/nonesuch" || f[0].RefKind != RefKindWaypoint {
		t.Errorf("broken's finding = %+v, want one naming Waypoint broken/nonesuch", f)
	}
}

// An object only a waypoint can honour, where its targets have none: the L4
// data plane cannot evaluate it. One code, the kind in the message.
func TestL7WithoutWaypoint(t *testing.T) {
	l7Objects := func(namespace string) []Object {
		return []Object{
			authz(namespace, "http-allow", map[string]any{"app": "cart"},
				map[string]any{"to": []any{map[string]any{"operation": map[string]any{"methods": []any{"GET"}}}}}),
			authz(namespace, "jwt-allow", nil,
				map[string]any{"from": []any{map[string]any{"source": map[string]any{"requestPrincipals": []any{"*"}}}}}),
			authz(namespace, "header-allow", nil,
				map[string]any{"when": []any{map[string]any{"key": "request.headers[x-token]", "values": []any{"a"}}}}),
			obj(KindRequestAuthentication, namespace, "jwt", map[string]any{"jwtRules": []any{}}),
			obj(KindVirtualService, namespace, "routes", map[string]any{
				"hosts": []any{"cart"},
				"http":  []any{map[string]any{"route": []any{map[string]any{"destination": map[string]any{"host": "cart"}}}}},
			}),
			obj(KindDestinationRule, namespace, "pool", map[string]any{
				"host":          "cart",
				"trafficPolicy": map[string]any{"connectionPool": map[string]any{"http": map[string]any{"h2UpgradePolicy": "UPGRADE"}}},
			}),
			obj(KindWasmPlugin, namespace, "filter", map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "cart"}}}),
			// L4 only: never this finding.
			authz(namespace, "l4-allow", map[string]any{"app": "cart"},
				map[string]any{"to": []any{map[string]any{"operation": map[string]any{"ports": []any{"8080"}}}}}),
			obj(KindVirtualService, namespace, "tcp-routes", map[string]any{
				"hosts": []any{"cart"},
				"tcp":   []any{map[string]any{"route": []any{map[string]any{"destination": map[string]any{"host": "cart"}}}}},
			}),
			// Bound to the waypoint itself, or to an ingress: L7 by definition.
			authz(namespace, "on-gateway", nil, map[string]any{"to": []any{map[string]any{"operation": map[string]any{"paths": []any{"/admin"}}}}}),
		}
	}
	withTarget := func(objects []Object) []Object {
		for i := range objects {
			if objects[i].Name == "on-gateway" {
				objects[i].Spec["targetRefs"] = []any{map[string]any{"kind": KindGateway, "name": "wp"}}
			}
		}
		return objects
	}
	waypoint := gateway("amb", "wp", "istio-waypoint", nil)
	cartSvc := service("amb", "cart", nil, map[string]any{"app": "cart"})
	cartPod := running("amb", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"})
	l7 := []string{"http-allow", "jwt-allow", "header-allow", "jwt", "routes", "pool", "filter"}

	t.Run("no waypoint anywhere", func(t *testing.T) {
		snap := judge(Snapshot{
			Namespaces: []Namespace{ambientNS},
			Objects:    append(withTarget(l7Objects("amb")), waypoint, cartSvc),
			Pods:       []Pod{cartPod},
		})
		for _, name := range l7 {
			var found bool
			for _, o := range snap.Objects {
				if o.Name == name && hasCode(o.Findings, CodeL7WithoutWaypoint) {
					found = true
					for _, f := range o.Findings {
						if f.Code == CodeL7WithoutWaypoint && !strings.HasPrefix(f.Message, o.Kind+" ") {
							t.Errorf("%s message = %q, want it to open with the kind", name, f.Message)
						}
					}
				}
			}
			if !found {
				t.Errorf("%s in an ambient namespace with no waypoint was not flagged", name)
			}
		}
		if got := codes(snap)[CodeL7WithoutWaypoint]; got != len(l7) {
			t.Errorf("L7 findings = %d, want %d — an L4 object or the gateway-bound policy was flagged", got, len(l7))
		}
	})
	t.Run("namespace waypoint", func(t *testing.T) {
		ns := ambientNS
		ns.Waypoint, ns.WaypointNamespace = "wp", "amb"
		snap := judge(Snapshot{
			Namespaces: []Namespace{ns},
			Objects:    append(withTarget(l7Objects("amb")), waypoint, cartSvc),
			Pods:       []Pod{cartPod},
		})
		if got := codes(snap)[CodeL7WithoutWaypoint]; got != 0 {
			t.Errorf("L7 findings = %d under a namespace waypoint, want none", got)
		}
	})
	t.Run("workload and service waypoint", func(t *testing.T) {
		bound := cartPod
		bound.Labels = map[string]string{"app": "cart", labelUseWaypoint: "wp"}
		boundSvc := service("amb", "cart", map[string]string{labelUseWaypoint: "wp"}, map[string]any{"app": "cart"})
		// Only the selector- and host-scoped objects can be answered by the
		// workload and the service; the namespace-wide policies still have
		// unbound targets (none, here, but no binding to point at either).
		snap := judge(Snapshot{
			Namespaces: []Namespace{ambientNS},
			Objects:    append(withTarget(l7Objects("amb")), waypoint, boundSvc),
			Pods:       []Pod{bound},
		})
		for _, name := range []string{"http-allow", "routes", "pool", "filter"} {
			for _, o := range snap.Objects {
				if o.Name == name && hasCode(o.Findings, CodeL7WithoutWaypoint) {
					t.Errorf("%s was flagged though every target it selects has a waypoint", name)
				}
			}
		}
	})
	t.Run("sidecar namespace", func(t *testing.T) {
		snap := judge(Snapshot{
			Namespaces: []Namespace{{Name: "sc", DataplaneMode: DataplaneSidecar}},
			Objects:    append(l7Objects("sc"), service("sc", "cart", nil, map[string]any{"app": "cart"})),
			Pods:       []Pod{withSidecar(running("sc", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"}))},
		})
		if got := codes(snap)[CodeL7WithoutWaypoint]; got != 0 {
			t.Errorf("L7 findings = %d in a sidecar namespace, want none", got)
		}
	})
}
