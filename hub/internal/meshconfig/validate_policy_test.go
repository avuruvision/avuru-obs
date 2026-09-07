package meshconfig

import (
	"strings"
	"testing"
)

// authz is an AuthorizationPolicy selecting workloads by labels, with rules.
func authz(namespace, name string, selector map[string]any, rules ...map[string]any) Object {
	spec := map[string]any{}
	if selector != nil {
		spec["selector"] = map[string]any{"matchLabels": selector}
	}
	if len(rules) > 0 {
		items := make([]any, 0, len(rules))
		for _, r := range rules {
			items = append(items, r)
		}
		spec["rules"] = items
	}
	return obj(KindAuthorizationPolicy, namespace, name, spec)
}

func fromPrincipals(principals ...any) map[string]any {
	return map[string]any{"from": []any{map[string]any{"source": map[string]any{"principals": principals}}}}
}

// A policy whose selector matches nothing protects nothing, and reads as
// protection. Judged from pods — so silent, and said so, when they were cut.
func TestPolicyNoMatch(t *testing.T) {
	pods := []Pod{running("shop", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"})}
	objects := []Object{
		authz("shop", "matches", map[string]any{"app": "cart"}),
		authz("shop", "matches-nothing", map[string]any{"app": "cart", "tier": "gold"}),
		obj(KindSidecar, "shop", "egress-nothing", map[string]any{
			"workloadSelector": map[string]any{"labels": map[string]any{"app": "nonesuch"}}}),
		// Namespace-wide: nothing to match, nothing to flag.
		authz("shop", "deny-all", nil),
		// Same labels, other namespace: a selector never reaches across.
		authz("web", "wrong-namespace", map[string]any{"app": "cart"}),
	}
	for _, tc := range []struct {
		name     string
		snap     Snapshot
		want     int
		skipped  bool
		unmapped []string
	}{
		{"pods readable", Snapshot{Objects: objects, Pods: pods}, 3, false, []string{"matches-nothing", "egress-nothing", "wrong-namespace"}},
		{"pods cut", Snapshot{Objects: objects, Pods: pods, PodsTruncated: true}, 0, true, nil},
		{"pods refused", Snapshot{Objects: objects, MissingKinds: []string{KindPod}}, 0, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := judge(tc.snap)
			if got := codes(snap)[CodePolicyNoMatch]; got != tc.want {
				t.Errorf("no-match findings = %d, want %d", got, tc.want)
			}
			if (snap.ChecksSkipped != "") != tc.skipped {
				t.Errorf("ChecksSkipped = %q, want skipped=%v", snap.ChecksSkipped, tc.skipped)
			}
			for _, name := range tc.unmapped {
				for _, o := range snap.Objects {
					if o.Name == name && !hasCode(o.Findings, CodePolicyNoMatch) {
						t.Errorf("%s was not flagged", name)
					}
				}
			}
			if f := objectFindings(snap, KindAuthorizationPolicy, "matches-nothing"); len(f) == 1 && !strings.Contains(f[0].Message, "app=cart,tier=gold") {
				t.Errorf("message = %q, want it to spell the selector", f[0].Message)
			}
		})
	}
}

// A targetRef naming nothing is the same finding from the other side, and it
// needs no pods.
func TestPolicyTargetRefMissing(t *testing.T) {
	snap := Validate(Snapshot{
		MissingKinds: []string{KindPod},
		Objects: []Object{
			obj(KindGateway, "shop", "waypoint", map[string]any{"gatewayClassName": "istio-waypoint"}),
			svc("shop", "cart"),
			obj(KindAuthorizationPolicy, "shop", "on-waypoint", map[string]any{
				"targetRefs": []any{map[string]any{"kind": KindGateway, "name": "waypoint"}}}),
			obj(KindAuthorizationPolicy, "shop", "on-service", map[string]any{
				"targetRef": map[string]any{"kind": KindService, "name": "cart"}}),
			obj(KindAuthorizationPolicy, "shop", "on-nothing", map[string]any{
				"targetRefs": []any{map[string]any{"kind": KindGateway, "name": "nonesuch"}}}),
			obj(KindAuthorizationPolicy, "shop", "on-a-class", map[string]any{
				"targetRefs": []any{map[string]any{"kind": "GatewayClass", "name": "istio"}}}),
		},
	})
	if got := codes(snap)[CodePolicyNoMatch]; got != 1 {
		t.Errorf("no-match findings = %d, want only on-nothing", got)
	}
	f := objectFindings(snap, KindAuthorizationPolicy, "on-nothing")
	if len(f) != 1 || f[0].Ref != "shop/nonesuch" || f[0].RefKind != RefKindGateway {
		t.Errorf("on-nothing findings = %+v, want one naming Gateway shop/nonesuch", f)
	}
}

// A rule for a principal nobody runs as decides nothing: an ALLOW for it
// allows nobody, a DENY denies nobody.
func TestPrincipalUnknown(t *testing.T) {
	cart := running("shop", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"})
	cart.ServiceAccount = "cart"
	// A pod with no service account named runs as default.
	plain := running("shop", "plain-0", KindStatefulSet, "plain", nil)
	plain.ServiceAccount = ""
	pods := []Pod{cart, plain}
	policy := authz("shop", "callers", nil,
		fromPrincipals("cluster.local/ns/shop/sa/cart", "spiffe://cluster.local/ns/shop/sa/default"),
		fromPrincipals("*/ns/shop/sa/nonesuch", "cluster.local/ns/shop/sa/*", "cluster.local/ns/*/sa/cart"),
		// The same unknown principal again: one finding, not two.
		fromPrincipals("cluster.local/ns/shop/sa/nonesuch"),
	)
	for _, tc := range []struct {
		name string
		snap Snapshot
		want int
	}{
		{"pods readable", Snapshot{Objects: []Object{policy}, Pods: pods}, 1},
		{"pods cut", Snapshot{Objects: []Object{policy}, Pods: pods, PodsTruncated: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := judge(tc.snap)
			f := objectFindings(snap, KindAuthorizationPolicy, "callers")
			var got []Finding
			for _, x := range f {
				if x.Code == CodePrincipalUnknown {
					got = append(got, x)
				}
			}
			if len(got) != tc.want {
				t.Fatalf("principal findings = %+v, want %d", got, tc.want)
			}
			if tc.want == 1 && (got[0].Ref != "shop/nonesuch" || got[0].RefKind != RefKindServiceAccount) {
				t.Errorf("ref = %s %q, want ServiceAccount shop/nonesuch", got[0].RefKind, got[0].Ref)
			}
		})
	}
}
