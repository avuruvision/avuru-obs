package meshconfig

import (
	"slices"
	"strings"
	"testing"
)

func obj(kind, namespace, name string, spec map[string]any) Object {
	return Object{Kind: kind, Namespace: namespace, Name: name, Spec: spec}
}

func svc(namespace, name string) Object { return obj(KindService, namespace, name, nil) }

func route(namespace, name string, parents, backends []any) Object {
	return obj(KindHTTPRoute, namespace, name, map[string]any{
		"parentRefs": parents,
		"rules":      []any{map[string]any{"backendRefs": backends}},
	})
}

func ref(name string, extra map[string]any) map[string]any {
	m := map[string]any{"name": name}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// codes collects every finding raised anywhere in the snapshot — on objects,
// namespaces, workloads and services alike.
func codes(snap Snapshot) map[Code]int {
	out := map[Code]int{}
	for _, f := range allFindings(snap) {
		out[f.Code]++
	}
	return out
}

func allFindings(snap Snapshot) []Finding {
	var out []Finding
	for _, o := range snap.Objects {
		out = append(out, o.Findings...)
	}
	for _, ns := range snap.Namespaces {
		out = append(out, ns.Findings...)
	}
	for _, w := range snap.Workloads {
		out = append(out, w.Findings...)
	}
	for _, s := range snap.Services {
		out = append(out, s.Findings...)
	}
	return out
}

// judge derives the workload and service inventory the reader would, then
// validates — the same two steps build() takes, so a test hands in pods and
// objects and gets the findings a real snapshot would carry. Objects are
// copied first: Validate writes findings in place, and a test that judges one
// object list twice must not see the first run's findings on the second.
func judge(snap Snapshot) Snapshot {
	snap.Objects = slices.Clone(snap.Objects)
	for i := range snap.Objects {
		snap.Objects[i].Findings = nil
	}
	snap.Services = ServicesFrom(snap.Objects, snap.Pods, snap.Namespaces)
	snap.Workloads = WorkloadsFrom(snap.Pods, snap.Namespaces, snap.Objects, "istio-system")
	return Validate(snap)
}

// objectFindings returns the findings on one object, by kind and name.
func objectFindings(snap Snapshot, kind, name string) []Finding {
	for _, o := range snap.Objects {
		if o.Kind == kind && o.Name == name {
			return o.Findings
		}
	}
	return nil
}

func workloadFindings(snap Snapshot, id string) []Finding {
	for _, w := range snap.Workloads {
		if key(w.Namespace, w.Name) == id {
			return w.Findings
		}
	}
	return nil
}

func hasCode(findings []Finding, code Code) bool {
	for _, f := range findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// The check the whole module is worth having: a route that attaches, matches,
// and drops every request, emitting no span for any of them.
func TestRouteWithMissingBackend(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		obj(KindGateway, "istio-edge", "public", map[string]any{"gatewayClassName": "istio"}),
		svc("shop", "checkout"),
		route("shop", "web", []any{ref("public", map[string]any{"namespace": "istio-edge"})},
			[]any{ref("checkout", nil), ref("payments", nil)}),
	}})

	got := codes(snap)
	if got[CodeRouteBackendMissing] != 1 {
		t.Errorf("backend-missing findings = %d, want exactly the one for payments", got[CodeRouteBackendMissing])
	}
	if got[CodeRouteParentMissing] != 0 {
		t.Errorf("a Gateway that exists was reported missing")
	}
	// The finding must name the thing that is absent, and what kind of thing
	// it is, so it can be searched for on the right list.
	for _, o := range snap.Objects {
		for _, f := range o.Findings {
			if f.Code == CodeRouteBackendMissing && (f.Ref != "shop/payments" || f.RefKind != RefKindService) {
				t.Errorf("finding ref = %s %q, want Service shop/payments", f.RefKind, f.Ref)
			}
		}
	}
}

func TestRouteWithMissingParent(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		svc("shop", "checkout"),
		route("shop", "web", []any{ref("nonesuch", nil)}, []any{ref("checkout", nil)}),
	}})
	if codes(snap)[CodeRouteParentMissing] != 1 {
		t.Error("a parentRef naming no Gateway was not reported")
	}
}

// A backendRef may legitimately name something that is not a Service, and a
// route may attach to a Service rather than a Gateway in ambient mesh mode.
// Neither is ours to resolve, and flagging them would be noise.
func TestNonServiceReferencesAreLeftAlone(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		obj(KindHTTPRoute, "shop", "mesh-route", map[string]any{
			"parentRefs": []any{ref("checkout", map[string]any{"kind": "Service"})},
			"rules": []any{map[string]any{"backendRefs": []any{
				ref("other-route", map[string]any{"kind": "HTTPRoute"}),
			}}},
		}),
	}})
	if len(codes(snap)) != 0 {
		t.Errorf("non-Service references produced findings: %+v", codes(snap))
	}
}

// A waypoint is addressed by labels, never by parentRefs, so "no routes" is its
// ordinary state. Flagging it would put a warning on every ambient install.
func TestWaypointIsNotAnUnusedGateway(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		obj(KindGateway, "istio-waypoint", "global-waypoint", map[string]any{"gatewayClassName": "istio-waypoint"}),
		obj(KindGateway, "istio-edge", "public", map[string]any{"gatewayClassName": "istio"}),
	}})
	got := codes(snap)
	if got[CodeGatewayNoRoutes] != 1 {
		t.Errorf("unused-gateway findings = %d, want only the real gateway", got[CodeGatewayNoRoutes])
	}
	if hasCode(objectFindings(snap, KindGateway, "global-waypoint"), CodeGatewayNoRoutes) {
		t.Error("a waypoint was flagged for having no routes")
	}
}

// Two objects that each look fine alone and cannot both be honoured.
func TestMTLSConflict(t *testing.T) {
	snap := Validate(Snapshot{
		Namespaces: []Namespace{{Name: "shop", MTLSMode: "STRICT"}, {Name: "lab", MTLSMode: "PERMISSIVE"}},
		Objects: []Object{
			svc("shop", "checkout"),
			svc("lab", "sandbox"),
			obj(KindDestinationRule, "shop", "no-tls", map[string]any{
				"host":          "checkout.shop.svc.cluster.local",
				"trafficPolicy": map[string]any{"tls": map[string]any{"mode": "DISABLE"}},
			}),
			// Same shape, in a permissive namespace: no conflict.
			obj(KindDestinationRule, "lab", "no-tls", map[string]any{
				"host":          "sandbox.lab.svc.cluster.local",
				"trafficPolicy": map[string]any{"tls": map[string]any{"mode": "DISABLE"}},
			}),
		},
	})
	if got := codes(snap)[CodeMTLSConflict]; got != 1 {
		t.Errorf("mTLS conflicts = %d, want exactly the strict namespace's", got)
	}
}

// Host resolution must be conservative: a false "does not exist" on a working
// external route teaches an operator to ignore the whole column.
func TestHostResolutionIsConservative(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		svc("shop", "checkout"),
		obj(KindServiceEntry, "shop", "stripe", map[string]any{"hosts": []any{"api.stripe.com"}}),
		// Resolvable, four ways.
		obj(KindVirtualService, "shop", "short", map[string]any{"hosts": []any{"checkout"}}),
		obj(KindVirtualService, "shop", "fqdn", map[string]any{"hosts": []any{"checkout.shop.svc.cluster.local"}}),
		obj(KindVirtualService, "shop", "entry", map[string]any{"hosts": []any{"api.stripe.com"}}),
		obj(KindVirtualService, "shop", "wild", map[string]any{"hosts": []any{"*.example.com"}}),
		// External and unknown to us: not ours to doubt.
		obj(KindVirtualService, "shop", "external", map[string]any{"hosts": []any{"payments.partner.io"}}),
		// Cluster-shaped and absent: this one is a real finding.
		obj(KindVirtualService, "shop", "typo", map[string]any{"hosts": []any{"chekout.shop.svc.cluster.local"}}),
	}})

	if got := codes(snap)[CodeHostUnresolved]; got != 1 {
		t.Errorf("unresolved hosts = %d, want only the cluster-shaped typo", got)
	}
	for _, o := range snap.Objects {
		if o.Name == "typo" && len(o.Findings) == 0 {
			t.Error("the typo was not caught")
		}
	}
}

// A bare host means a different Service in every namespace, so it resolves
// against the asker's — never against whichever namespace happened to be
// indexed last.
func TestBareHostResolvesInTheAskersNamespace(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		svc("web", "checkout"),
		obj(KindVirtualService, "shop", "bare", map[string]any{"hosts": []any{"checkout"}}),
		obj(KindVirtualService, "web", "bare", map[string]any{"hosts": []any{"checkout"}}),
	}})
	for _, o := range snap.Objects {
		if o.Kind != KindVirtualService {
			continue
		}
		unresolved := hasCode(o.Findings, CodeHostUnresolved)
		if want := o.Namespace == "shop"; unresolved != want {
			t.Errorf("%s/%s unresolved = %v, want %v", o.Namespace, o.Name, unresolved, want)
		}
	}
}

// The pod-dependent checks go silent when pods were refused or cut, and the
// snapshot says so: an empty issues column must never read as a clean bill.
func TestChecksSkippedNamesWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap Snapshot
		want string
	}{
		{"pods readable", Snapshot{}, ""},
		{"pods refused", Snapshot{MissingKinds: []string{KindPod}, MissingReasons: map[string]string{KindPod: reasonForbidden}},
			"pods were not readable (the ClusterRole does not grant reading it) — grant pods get, list and watch"},
		{"pods cut", Snapshot{PodsTruncated: true, Pods: []Pod{{Name: "a"}, {Name: "b"}}}, "the pod list was cut at 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Validate(tc.snap).ChecksSkipped
			if tc.want == "" && got != "" {
				t.Fatalf("ChecksSkipped = %q, want none", got)
			}
			if tc.want != "" && (!strings.Contains(got, tc.want) || !strings.Contains(got, string(CodeAmbientNotEnrolled))) {
				t.Errorf("ChecksSkipped = %q, want it to name the checks and say %q", got, tc.want)
			}
		})
	}
}
