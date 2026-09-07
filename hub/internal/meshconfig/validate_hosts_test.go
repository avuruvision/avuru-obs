package meshconfig

import "testing"

func vs(namespace, name string, hosts []any, extra map[string]any) Object {
	spec := map[string]any{"hosts": hosts}
	for k, v := range extra {
		spec[k] = v
	}
	return obj(KindVirtualService, namespace, name, spec)
}

func dr(namespace, name, host string, subsets []any, trafficPolicy map[string]any) Object {
	spec := map[string]any{"host": host}
	if subsets != nil {
		spec["subsets"] = subsets
	}
	if trafficPolicy != nil {
		spec["trafficPolicy"] = trafficPolicy
	}
	return obj(KindDestinationRule, namespace, name, spec)
}

func subset(name string) map[string]any {
	return map[string]any{"name": name, "labels": map[string]any{"version": name}}
}

func routeTo(host, subset string) map[string]any {
	return map[string]any{"route": []any{map[string]any{"destination": map[string]any{"host": host, "subset": subset}}}}
}

// A route to a subset no DestinationRule defines matches and has no upstream:
// the request fails at the proxy and the destination never sees it.
func TestSubsetMissing(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		svc("shop", "cart"),
		svc("shop", "pay"),
		dr("shop", "cart", "cart", []any{subset("v1")}, nil),
		dr("shop", "pay", "pay", []any{subset("v1")}, nil),
		vs("shop", "defined", []any{"cart"}, map[string]any{"http": []any{routeTo("cart", "v1")}}),
		vs("shop", "undefined", []any{"pay"}, map[string]any{
			"http": []any{routeTo("pay.shop.svc.cluster.local", "v2")},
			// The same missing subset again, on a TCP route: one finding.
			"tcp": []any{routeTo("pay", "v2")},
		}),
		// Unresolved host: that is checkHost's finding, and only that one.
		vs("shop", "typo", []any{"cartt"}, map[string]any{"http": []any{routeTo("cartt", "v1")}}),
	}})
	if got := hasCode(objectFindings(snap, KindVirtualService, "defined"), CodeSubsetMissing); got {
		t.Error("a defined subset was reported missing")
	}
	f := objectFindings(snap, KindVirtualService, "undefined")
	if len(f) != 1 || f[0].Code != CodeSubsetMissing || f[0].RefKind != RefKindDestinationRule {
		t.Errorf("undefined subset findings = %+v, want exactly one, naming a DestinationRule", f)
	}
	f = objectFindings(snap, KindVirtualService, "typo")
	if len(f) != 1 || f[0].Code != CodeHostUnresolved {
		t.Errorf("typo findings = %+v, want only the unresolved host", f)
	}
}

// Two rules claiming one host: only one is applied, and the other looks
// configured and does nothing. Each names the other.
func TestHostConflict(t *testing.T) {
	snap := Validate(Snapshot{Objects: []Object{
		svc("shop", "cart"),
		svc("shop", "pay"),
		svc("shop", "web"),
		// Two mesh-bound VirtualServices for one host, spelled two ways.
		vs("shop", "cart-a", []any{"cart"}, nil),
		vs("shop", "cart-b", []any{"cart.shop.svc.cluster.local"}, map[string]any{"gateways": []any{"mesh"}}),
		// Two for one host on distinct gateways: not the mesh's, no conflict.
		vs("shop", "pay-edge", []any{"pay"}, map[string]any{"gateways": []any{"istio-edge/public"}}),
		vs("shop", "pay-mesh", []any{"pay"}, nil),
		// DestinationRules: disjoint subsets coexist, overlapping ones and two
		// top-level trafficPolicies do not.
		dr("shop", "web-v1", "web", []any{subset("v1")}, nil),
		dr("shop", "web-v2", "web", []any{subset("v2")}, nil),
		dr("shop", "web-v2-again", "web", []any{subset("v2"), subset("v3")}, nil),
		dr("shop", "pay-a", "pay", nil, map[string]any{"tls": map[string]any{"mode": "ISTIO_MUTUAL"}}),
		dr("shop", "pay-b", "pay", nil, map[string]any{"loadBalancer": map[string]any{"simple": "ROUND_ROBIN"}}),
	}})
	for _, tc := range []struct {
		kind, name string
		wantRefs   []string
	}{
		{KindVirtualService, "cart-a", []string{"shop/cart-b"}},
		{KindVirtualService, "cart-b", []string{"shop/cart-a"}},
		{KindVirtualService, "pay-edge", nil},
		{KindVirtualService, "pay-mesh", nil},
		{KindDestinationRule, "web-v1", nil},
		{KindDestinationRule, "web-v2", []string{"shop/web-v2-again"}},
		{KindDestinationRule, "web-v2-again", []string{"shop/web-v2"}},
		{KindDestinationRule, "pay-a", []string{"shop/pay-b"}},
		{KindDestinationRule, "pay-b", []string{"shop/pay-a"}},
	} {
		var refs []string
		for _, f := range objectFindings(snap, tc.kind, tc.name) {
			if f.Code == CodeHostConflict {
				refs = append(refs, f.Ref)
			}
		}
		if len(refs) != len(tc.wantRefs) {
			t.Errorf("%s %s conflicts with %v, want %v", tc.kind, tc.name, refs, tc.wantRefs)
			continue
		}
		for i := range refs {
			if refs[i] != tc.wantRefs[i] {
				t.Errorf("%s %s conflicts with %v, want %v", tc.kind, tc.name, refs, tc.wantRefs)
			}
		}
	}
}

// The mTLS conflict is judged against each workload's EFFECTIVE policy — a
// selector PeerAuthentication now counts — and in both directions: TLS
// disabled toward a workload that requires it, and mutual TLS demanded of one
// that disables it.
func TestMTLSConflictPerWorkload(t *testing.T) {
	objects := []Object{
		pa("shop", "default", "STRICT", false),
		selectorPA("shop", "legacy-off", "DISABLE", map[string]any{"app": "legacy"}),
		service("shop", "cart", nil, map[string]any{"app": "cart"}),
		service("shop", "legacy", nil, map[string]any{"app": "legacy"}),
		// Disabled toward a strict workload: conflict, naming the workload.
		dr("shop", "cart-plain", "cart", nil, map[string]any{"tls": map[string]any{"mode": "DISABLE"}}),
		// Disabled toward a workload whose own policy disables it: fine,
		// though the namespace is STRICT — this is the case v0.14 got wrong.
		dr("shop", "legacy-plain", "legacy", nil, map[string]any{"tls": map[string]any{"mode": "DISABLE"}}),
		// Mutual TLS demanded of the workload that disables it: the reverse.
		dr("shop", "legacy-mutual", "legacy", nil, map[string]any{"tls": map[string]any{"mode": "ISTIO_MUTUAL"}}),
		dr("shop", "cart-mutual", "cart", nil, map[string]any{"tls": map[string]any{"mode": "ISTIO_MUTUAL"}}),
	}
	pods := []Pod{
		running("shop", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"}),
		running("shop", "legacy-0", KindStatefulSet, "legacy", map[string]string{"app": "legacy"}),
	}
	ns := NamespacesFrom([]Object{{Kind: KindNamespace, Name: "shop"}}, objects, "istio-system")
	snap := judge(Snapshot{Namespaces: ns, Objects: objects, Pods: pods})

	for name, want := range map[string]string{
		"cart-plain": "shop/cart", "legacy-plain": "", "legacy-mutual": "shop/legacy", "cart-mutual": "",
	} {
		var refs []string
		for _, f := range objectFindings(snap, KindDestinationRule, name) {
			if f.Code == CodeMTLSConflict {
				refs = append(refs, f.RefKind+" "+f.Ref)
			}
		}
		switch {
		case want == "" && len(refs) != 0:
			t.Errorf("%s: conflicts %v, want none", name, refs)
		case want != "" && (len(refs) != 1 || refs[0] != RefKindWorkload+" "+want):
			t.Errorf("%s: conflicts %v, want one on Workload %s", name, refs, want)
		}
	}
}
