package meshconfig

import (
	"reflect"
	"testing"
)

// running is a Running pod owned by a controller, with an app container.
func running(namespace, name, ownerKind, ownerName string, labels map[string]string) Pod {
	return Pod{
		Namespace: namespace, Name: name, Labels: labels,
		OwnerKind: ownerKind, OwnerName: ownerName,
		ServiceAccount: name + "-sa", Containers: []string{"app"}, Phase: phaseRunning,
	}
}

func withSidecar(p Pod) Pod {
	p.Containers = append(p.Containers, containerIstioProxy)
	return p
}

func captured(p Pod) Pod {
	p.Annotations = map[string]string{annotationAmbientRedirection: valueInjectEnabled}
	return p
}

func byWorkload(ws []Workload) map[string]Workload {
	out := map[string]Workload{}
	for _, w := range ws {
		out[key(w.Namespace, w.Name)] = w
	}
	return out
}

// A pod is joined to its workload without a ReplicaSet watch: the template
// hash names the Deployment when one exists, and when it cannot be confirmed
// the pod is reported as its ReplicaSet's — true, rather than a guess.
func TestWorkloadOwnerResolution(t *testing.T) {
	hash := map[string]string{"app": "cart", labelPodTemplateHash: "7d9f"}
	pods := []Pod{
		running("shop", "cart-7d9f-a", KindReplicaSet, "cart-7d9f", hash),
		running("shop", "cart-7d9f-b", KindReplicaSet, "cart-7d9f", hash),
		// Same shape, no Deployment read for it.
		running("shop", "orphan-7d9f-a", KindReplicaSet, "orphan-7d9f", map[string]string{labelPodTemplateHash: "7d9f"}),
		running("shop", "db-0", KindStatefulSet, "db", map[string]string{"app": "db"}),
		running("shop", "debug", "", "", map[string]string{"run": "debug"}),
	}
	objects := []Object{obj(KindDeployment, "shop", "cart", nil)}

	got := byWorkload(WorkloadsFrom(pods, nil, objects, "istio-system"))
	for id, want := range map[string]struct {
		kind string
		pods int
	}{
		"shop/cart":        {KindDeployment, 2},
		"shop/orphan-7d9f": {KindReplicaSet, 1},
		"shop/db":          {KindStatefulSet, 1},
		"shop/debug":       {KindPod, 1},
	} {
		w, ok := got[id]
		if !ok {
			t.Errorf("%s missing from %v", id, got)
			continue
		}
		if w.Kind != want.kind || w.Pods != want.pods {
			t.Errorf("%s = %s with %d pods, want %s with %d", id, w.Kind, w.Pods, want.kind, want.pods)
		}
	}
	if got["shop/cart"].ServiceAccount != "cart-7d9f-a-sa" {
		t.Errorf("service account = %q, want the first pod's by name", got["shop/cart"].ServiceAccount)
	}
}

// Enrolment is a fact about a RUNNING pod. A Pending pod has no sidecar yet,
// and counting it would report a workload as enrolled before it is.
func TestWorkloadEnrolmentFromPods(t *testing.T) {
	pending := withSidecar(running("shop", "web-0", KindStatefulSet, "web", nil))
	pending.Phase = "Pending"
	pods := []Pod{
		withSidecar(running("shop", "cart-0", KindStatefulSet, "cart", nil)),
		captured(running("shop", "pay-0", KindStatefulSet, "pay", nil)),
		running("shop", "plain-0", KindStatefulSet, "plain", nil),
		pending,
	}
	got := byWorkload(WorkloadsFrom(pods, nil, nil, "istio-system"))
	for id, want := range map[string]struct {
		injected, captured bool
		mode               string
		running            int
	}{
		"shop/cart":  {true, false, DataplaneSidecar, 1},
		"shop/pay":   {false, true, DataplaneAmbient, 1},
		"shop/plain": {false, false, "", 1},
		"shop/web":   {false, false, "", 0},
	} {
		w := got[id]
		if w.Injected != want.injected || w.Captured != want.captured || w.DataplaneMode != want.mode || w.RunningPods != want.running {
			t.Errorf("%s = injected %v captured %v mode %q running %d, want %+v", id, w.Injected, w.Captured, w.DataplaneMode, w.RunningPods, want)
		}
	}
}

// What was asked for, under the mesh's precedence: the pod's own label beats
// the namespace's, and an injection opt-out turns a sidecar namespace into
// nothing for that pod.
func TestWorkloadDeclaredMode(t *testing.T) {
	ambient := Namespace{Name: "amb", DataplaneMode: DataplaneAmbient}
	sidecar := Namespace{Name: "sc", DataplaneMode: DataplaneSidecar}
	for _, tc := range []struct {
		name        string
		ns          Namespace
		labels      map[string]string
		annotations map[string]string
		want        string
	}{
		{"inherits ambient", ambient, nil, nil, DataplaneAmbient},
		{"inherits sidecar", sidecar, nil, nil, DataplaneSidecar},
		{"pod none beats ambient namespace", ambient, map[string]string{labelDataplaneMode: "none"}, nil, ""},
		{"pod ambient in an unlabelled namespace", Namespace{Name: "amb"}, map[string]string{labelDataplaneMode: "ambient"}, nil, DataplaneAmbient},
		{"inject=false label opts out of sidecar", sidecar, map[string]string{labelSidecarInject: "false"}, nil, ""},
		{"inject=false annotation opts out of sidecar", sidecar, nil, map[string]string{annotationSidecarInject: "false"}, ""},
		{"inject=false does not touch ambient", ambient, map[string]string{labelSidecarInject: "false"}, nil, DataplaneAmbient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := running(tc.ns.Name, "x-0", KindStatefulSet, "x", tc.labels)
			p.Annotations = tc.annotations
			got := WorkloadsFrom([]Pod{p}, []Namespace{tc.ns}, nil, "istio-system")
			if got[0].DeclaredMode != tc.want {
				t.Errorf("declared = %q, want %q", got[0].DeclaredMode, tc.want)
			}
		})
	}
}

// A waypoint binding is the pod's own when it carries the label, the
// namespace's otherwise — and "none" on the pod stops the namespace's from
// reaching it.
func TestWorkloadWaypointPrecedence(t *testing.T) {
	ns := Namespace{Name: "shop", Waypoint: "shop-waypoint", WaypointNamespace: "shop"}
	pods := []Pod{
		running("shop", "a-0", KindStatefulSet, "a", nil),
		running("shop", "b-0", KindStatefulSet, "b", map[string]string{labelUseWaypoint: "global", labelWaypointNS: "istio-waypoint"}),
		running("shop", "c-0", KindStatefulSet, "c", map[string]string{labelUseWaypoint: "none"}),
		running("shop", "d-0", KindStatefulSet, "d", map[string]string{labelUseWaypoint: "local"}),
	}
	got := byWorkload(WorkloadsFrom(pods, []Namespace{ns}, nil, "istio-system"))
	for id, want := range map[string][3]string{
		"shop/a": {"shop-waypoint", "shop", SourceNamespace},
		"shop/b": {"global", "istio-waypoint", SourceWorkload},
		"shop/c": {"", "", SourceWorkload},
		"shop/d": {"local", "shop", SourceWorkload},
	} {
		w := got[id]
		if got := [3]string{w.Waypoint, w.WaypointNamespace, w.WaypointSource}; got != want {
			t.Errorf("%s = %v, want %v", id, got, want)
		}
	}
}

// Every policy that covers a workload, with the scope it reached it at — and
// a policy bound by targetRefs belongs to the object it names, not here.
func TestWorkloadPoliciesAndMTLS(t *testing.T) {
	labels := map[string]string{"app": "cart"}
	objects := []Object{
		pa("istio-system", "default", "STRICT", false),
		selectorPA("shop", "cart-permissive", "PERMISSIVE", map[string]any{"app": "cart"}),
		obj(KindAuthorizationPolicy, "shop", "deny-all", map[string]any{}),
		obj(KindAuthorizationPolicy, "shop", "cart-only", map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "cart"}}}),
		obj(KindAuthorizationPolicy, "shop", "other-only", map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "other"}}}),
		obj(KindAuthorizationPolicy, "shop", "on-gateway", map[string]any{
			"targetRefs": []any{map[string]any{"kind": KindGateway, "name": "edge"}}}),
		obj(KindSidecar, "shop", "cart-egress", map[string]any{
			"workloadSelector": map[string]any{"labels": map[string]any{"app": "cart"}}}),
		obj(KindTelemetry, "istio-system", "mesh-default", map[string]any{}),
		// A selector policy in another namespace never reaches across.
		obj(KindWasmPlugin, "web", "cart-plugin", map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "cart"}}}),
	}
	got := WorkloadsFrom([]Pod{running("shop", "cart-0", KindStatefulSet, "cart", labels)}, nil, objects, "istio-system")

	want := []PolicyRef{
		{KindAuthorizationPolicy, "shop", "cart-only", SourceWorkload},
		{KindAuthorizationPolicy, "shop", "deny-all", SourceNamespace},
		{KindPeerAuthentication, "istio-system", "default", SourceMesh},
		{KindPeerAuthentication, "shop", "cart-permissive", SourceWorkload},
		{KindSidecar, "shop", "cart-egress", SourceWorkload},
		{KindTelemetry, "istio-system", "mesh-default", SourceMesh},
	}
	if !reflect.DeepEqual(got[0].Policies, want) {
		t.Errorf("policies = %+v\nwant %+v", got[0].Policies, want)
	}
	if mtls := (DeclaredMTLS{Mode: "PERMISSIVE", Source: SourceWorkload, Policy: "shop/cart-permissive"}); got[0].DeclaredMTLS != mtls {
		t.Errorf("mTLS = %+v, want the selector policy %+v", got[0].DeclaredMTLS, mtls)
	}
}

// A workload knows the Services that select it, from the same resolution the
// Service list uses.
func TestWorkloadServicesCrossLinked(t *testing.T) {
	objects := []Object{
		obj(KindService, "shop", "cart", map[string]any{"selector": map[string]any{"app": "cart"}}),
		obj(KindService, "shop", "cart-admin", map[string]any{"selector": map[string]any{"app": "cart", "tier": "admin"}}),
		obj(KindService, "shop", "headless", nil),
	}
	pods := []Pod{
		running("shop", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"}),
		running("shop", "admin-0", KindStatefulSet, "admin", map[string]string{"app": "cart", "tier": "admin"}),
	}
	got := byWorkload(WorkloadsFrom(pods, nil, objects, "istio-system"))
	if want := []string{"shop/cart"}; !reflect.DeepEqual(got["shop/cart"].Services, want) {
		t.Errorf("cart services = %v, want %v", got["shop/cart"].Services, want)
	}
	if want := []string{"shop/cart", "shop/cart-admin"}; !reflect.DeepEqual(got["shop/admin"].Services, want) {
		t.Errorf("admin services = %v, want %v", got["shop/admin"].Services, want)
	}
}

// The namespace rows carry the count, and the gap between the two numbers is
// the one no label shows.
func TestCountWorkloads(t *testing.T) {
	namespaces := []Namespace{{Name: "shop"}, {Name: "empty"}}
	countWorkloads(namespaces, []Workload{
		{Namespace: "shop", Name: "a", Injected: true},
		{Namespace: "shop", Name: "b", Captured: true},
		{Namespace: "shop", Name: "c"},
		{Namespace: "unknown", Name: "d", Captured: true},
	})
	if namespaces[0].Workloads != 3 || namespaces[0].Enrolled != 2 {
		t.Errorf("shop = %d workloads, %d enrolled; want 3 and 2", namespaces[0].Workloads, namespaces[0].Enrolled)
	}
	if namespaces[1].Workloads != 0 || namespaces[1].Enrolled != 0 {
		t.Errorf("empty = %+v, want zeros", namespaces[1])
	}
}
