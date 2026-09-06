package meshconfig

import "testing"

// selectorPA is a PeerAuthentication scoped to the workloads carrying labels.
func selectorPA(namespace, name, mode string, labels map[string]any) Object {
	return Object{Kind: KindPeerAuthentication, Namespace: namespace, Name: name, Spec: map[string]any{
		"mtls":     map[string]any{"mode": mode},
		"selector": map[string]any{"matchLabels": labels},
	}}
}

// The precedence the mesh applies, and the source that says which level won.
// This is the resolver v0.14 skipped half of: it dropped every selector policy
// on the floor, so a workload under its own DISABLE read as STRICT.
func TestDeclaredMTLSPrecedence(t *testing.T) {
	policies := []Object{
		pa("istio-system", "default", "STRICT", false),
		pa("web", "default", "PERMISSIVE", false),
		selectorPA("web", "legacy", "DISABLE", map[string]any{"app": "legacy"}),
		// Two selector policies matching the same pod: the first by name
		// wins, and its name is surfaced so the choice is visible.
		selectorPA("web", "b-policy", "PERMISSIVE", map[string]any{"tier": "edge"}),
		selectorPA("web", "a-policy", "STRICT", map[string]any{"tier": "edge"}),
		// No mode at all: UNSET defers upward and must not shadow anything.
		{Kind: KindPeerAuthentication, Namespace: "shop", Name: "unset", Spec: map[string]any{}},
	}
	for _, tc := range []struct {
		name      string
		labels    map[string]string
		namespace string
		want      DeclaredMTLS
	}{
		{"selector policy beats the namespace's", map[string]string{"app": "legacy"}, "web",
			DeclaredMTLS{Mode: "DISABLE", Source: SourceWorkload, Policy: "web/legacy"}},
		{"namespace policy beats the mesh's", map[string]string{"app": "shop"}, "web",
			DeclaredMTLS{Mode: "PERMISSIVE", Source: SourceNamespace, Policy: "web/default"}},
		{"mesh policy reaches a namespace with none", map[string]string{"app": "shop"}, "shop",
			DeclaredMTLS{Mode: "STRICT", Source: SourceMesh, Policy: "istio-system/default"}},
		{"two selector policies: first by name", map[string]string{"tier": "edge"}, "web",
			DeclaredMTLS{Mode: "STRICT", Source: SourceWorkload, Policy: "web/a-policy"}},
		// A selector policy in another namespace does not reach across.
		{"selector policy is namespace-local", map[string]string{"app": "legacy"}, "shop",
			DeclaredMTLS{Mode: "STRICT", Source: SourceMesh, Policy: "istio-system/default"}},
		// The namespace itself: no labels, so only scope-wide policies count.
		{"namespace asks with no labels", nil, "web",
			DeclaredMTLS{Mode: "PERMISSIVE", Source: SourceNamespace, Policy: "web/default"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DeclaredMTLSFor(tc.labels, tc.namespace, policies, "istio-system")
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Nothing applies: empty, with no source and no policy, because the mesh
// default governs and we did not read it.
func TestDeclaredMTLSNothingApplies(t *testing.T) {
	got := DeclaredMTLSFor(map[string]string{"app": "x"}, "shop", []Object{
		selectorPA("shop", "other", "STRICT", map[string]any{"app": "y"}),
		pa("web", "default", "STRICT", false),
	}, "istio-system")
	if got != (DeclaredMTLS{}) {
		t.Errorf("got %+v, want nothing", got)
	}
}
