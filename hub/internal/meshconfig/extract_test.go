package meshconfig

import "testing"

func ns(name string, labels map[string]string) Object {
	return Object{Kind: KindNamespace, Name: name, Labels: labels}
}

func pa(namespace, name, mode string, selector bool) Object {
	spec := map[string]any{"mtls": map[string]any{"mode": mode}}
	if selector {
		spec["selector"] = map[string]any{"matchLabels": map[string]any{"app": "x"}}
	}
	return Object{Kind: KindPeerAuthentication, Namespace: namespace, Name: name, Spec: spec}
}

// The row that justifies the whole module: a namespace is in the mesh because
// of a LABEL, so this answer exists for a namespace that has never sent a byte.
func TestDataplaneModeFromLabels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"ambient", map[string]string{labelDataplaneMode: "ambient"}, DataplaneAmbient},
		{"ambient uppercase", map[string]string{labelDataplaneMode: "Ambient"}, DataplaneAmbient},
		{"sidecar", map[string]string{labelInjection: "enabled"}, DataplaneSidecar},
		// Revision-based injection names the revision instead of saying
		// "enabled", so presence is the signal.
		{"revision", map[string]string{labelRevision: "1-22"}, DataplaneSidecar},
		// Mid-migration: both labels present, and ambient is the one in effect.
		{"both", map[string]string{labelDataplaneMode: "ambient", labelInjection: "enabled"}, DataplaneAmbient},
		// Out of mesh is a real answer, not a missing one.
		{"none", map[string]string{"kubernetes.io/metadata.name": "x"}, ""},
		{"no labels", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := NamespacesFrom([]Object{ns("shop", tc.labels)}, nil, "istio-system")
			if rows[0].DataplaneMode != tc.want {
				t.Errorf("mode = %q, want %q", rows[0].DataplaneMode, tc.want)
			}
		})
	}
}

func TestWaypointBinding(t *testing.T) {
	rows := NamespacesFrom([]Object{
		ns("shop", map[string]string{labelUseWaypoint: "global-waypoint", labelWaypointNS: "istio-waypoint"}),
		// No explicit namespace: the waypoint is local, which is Istio's default.
		ns("web", map[string]string{labelUseWaypoint: "web-waypoint"}),
		// An explicit opt-OUT. Reporting "none" as a waypoint name would be
		// worse than reporting nothing.
		ns("batch", map[string]string{labelUseWaypoint: "none"}),
	}, nil, "istio-system")

	byName := map[string]Namespace{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if got := byName["shop"]; got.Waypoint != "global-waypoint" || got.WaypointNamespace != "istio-waypoint" {
		t.Errorf("shop waypoint = %q/%q", got.Waypoint, got.WaypointNamespace)
	}
	if got := byName["web"]; got.WaypointNamespace != "web" {
		t.Errorf("a local waypoint should default to its own namespace, got %q", got.WaypointNamespace)
	}
	if got := byName["batch"]; got.Waypoint != "" {
		t.Errorf("an opt-out was reported as a waypoint named %q", got.Waypoint)
	}
}

// mTLS is the question an operator asks about a namespace and cannot answer
// from traffic. Scope is the whole difficulty: mesh-wide, namespace, and
// workload policies are three different claims.
func TestMTLSModeScoping(t *testing.T) {
	namespaces := []Object{ns("shop", nil), ns("web", nil), ns("batch", nil)}
	policies := []Object{
		// Mesh-wide default, in the root namespace.
		pa("istio-system", "default", "STRICT", false),
		// Namespace override.
		pa("web", "default", "PERMISSIVE", false),
		// Workload-scoped: must NOT be reported as the namespace's mode, or
		// the row claims a setting most of the namespace does not have.
		pa("batch", "legacy-app", "DISABLE", true),
	}
	rows := NamespacesFrom(namespaces, policies, "istio-system")
	byName := map[string]Namespace{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	for name, want := range map[string]DeclaredMTLS{
		"shop": {Mode: "STRICT", Source: SourceMesh, Policy: "istio-system/default"},
		"web":  {Mode: "PERMISSIVE", Source: SourceNamespace, Policy: "web/default"},
		// The workload-scoped policy must not override the namespace: the row
		// keeps the mesh-wide mode, and says so.
		"batch": {Mode: "STRICT", Source: SourceMesh, Policy: "istio-system/default"},
	} {
		row := byName[name]
		got := DeclaredMTLS{Mode: row.MTLSMode, Source: row.MTLSSource, Policy: row.MTLSPolicy}
		if got != want {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
}

// Nothing configured at all means the mesh default governs, and we did not read
// it. Empty is honest; naming a mode would be a guess.
func TestNoPolicyLeavesModeUnstated(t *testing.T) {
	rows := NamespacesFrom([]Object{ns("shop", nil)}, nil, "istio-system")
	if rows[0].MTLSMode != "" || rows[0].MTLSSource != "" || rows[0].MTLSPolicy != "" {
		t.Errorf("row = %+v, want no mode, source or policy when nothing applies", rows[0])
	}
}

// A waypoint and an ingress gateway wear the same Gateway API label; only the
// gatewayClassName separates them, which is precisely what the telemetry-side
// classifier cannot see.
func TestWaypointDistinguishedByClass(t *testing.T) {
	waypoint := Object{Kind: KindGateway, Name: "global-waypoint", Spec: map[string]any{"gatewayClassName": "istio-waypoint"}}
	ingress := Object{Kind: KindGateway, Name: "public", Spec: map[string]any{"gatewayClassName": "istio"}}

	if !IsWaypoint(waypoint) {
		t.Error("a waypoint was not recognised from its gateway class")
	}
	if IsWaypoint(ingress) {
		t.Error("an ingress gateway was filed as a waypoint")
	}
	if got := WaypointScope(waypoint); got != "service" {
		t.Errorf("default scope = %q, want service (Istio's default)", got)
	}
	waypoint.Labels = map[string]string{labelWaypointForNS: "all"}
	if got := WaypointScope(waypoint); got != "all" {
		t.Errorf("scope = %q, want all", got)
	}
}
