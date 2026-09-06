package meshconfig

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// listKinds registers every resource the reader probes. The fake client PANICS
// on an unregistered one rather than returning NotFound the way a real API
// server does, so absence is simulated with reactors below instead.
func listKinds() map[schema.GroupVersionResource]string {
	m := map[schema.GroupVersionResource]string{}
	for _, w := range watched {
		m[w.gvr] = w.kind + "List"
	}
	return m
}

// seed creates objects through the client with an EXPLICIT GVR. Passing them to
// the constructor instead makes the fake guess the resource from the kind, and
// an object it cannot place is dropped silently — indistinguishable from a
// reader that failed to list it.
func seed(t *testing.T, c *dynamicfake.FakeDynamicClient, gvr schema.GroupVersionResource, objs ...*unstructured.Unstructured) {
	t.Helper()
	for _, o := range objs {
		ns := o.GetNamespace()
		var err error
		if ns == "" {
			_, err = c.Resource(gvr).Create(context.Background(), o, metav1.CreateOptions{})
		} else {
			_, err = c.Resource(gvr).Namespace(ns).Create(context.Background(), o, metav1.CreateOptions{})
		}
		if err != nil {
			t.Fatalf("seeding %s/%s: %v", gvr.Resource, o.GetName(), err)
		}
	}
}

var (
	gvrNamespaces = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	gvrPeerAuths  = schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "peerauthentications"}
	gvrGateways   = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
)

func u(apiVersion, kind, namespace, name string, labels map[string]string, spec map[string]any) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}}
	if namespace != "" {
		o.SetNamespace(namespace)
	}
	if labels != nil {
		o.SetLabels(labels)
	}
	if spec != nil {
		_ = unstructured.SetNestedMap(o.Object, spec, "spec")
	}
	return o
}

// The reader must produce namespace rows for namespaces that have sent no
// telemetry whatsoever — the whole reason the module exists.
func TestK8sReaderBuildsNamespacesFromLabelsAlone(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	seed(t, client, gvrNamespaces,
		u("v1", "Namespace", "", "shop", map[string]string{
			labelDataplaneMode: "ambient",
			labelUseWaypoint:   "global-waypoint",
			labelWaypointNS:    "istio-waypoint",
		}, nil),
		u("v1", "Namespace", "", "batch", nil, nil),
	)
	seed(t, client, gvrPeerAuths,
		u("security.istio.io/v1", "PeerAuthentication", "istio-system", "default", nil,
			map[string]any{"mtls": map[string]any{"mode": "STRICT"}}))
	seed(t, client, gvrGateways,
		u("gateway.networking.k8s.io/v1", "Gateway", "istio-waypoint", "global-waypoint", nil,
			map[string]any{"gatewayClassName": "istio-waypoint"}))

	r := NewK8sReader(context.Background(), client, "istio-system", "avuruobs-mesh-config")
	snap := r.Snapshot(context.Background())

	if snap.State != StateOK {
		t.Fatalf("state = %q (%s), want ok", snap.State, snap.Reason)
	}
	if snap.SyncedAt.IsZero() {
		t.Error("a successful read carries no timestamp")
	}
	byName := map[string]Namespace{}
	for _, n := range snap.Namespaces {
		byName[n.Name] = n
	}
	shop, ok := byName["shop"]
	if !ok {
		t.Fatalf("shop missing from %+v", snap.Namespaces)
	}
	if shop.DataplaneMode != DataplaneAmbient || shop.Waypoint != "global-waypoint" {
		t.Errorf("shop = %+v", shop)
	}
	// The mesh-wide policy reaches a namespace with no policy of its own.
	if byName["batch"].MTLSMode != "STRICT" {
		t.Errorf("batch mTLS = %q, want the mesh-wide STRICT", byName["batch"].MTLSMode)
	}
	// The gateway came through as an object; the namespaces did not double as
	// objects, which would have them counted twice on every screen.
	var gateways, namespaceObjects int
	for _, o := range snap.Objects {
		switch o.Kind {
		case KindGateway:
			gateways++
		case KindNamespace:
			namespaceObjects++
		}
	}
	if gateways != 1 {
		t.Errorf("gateways in objects = %d, want 1", gateways)
	}
	if namespaceObjects != 0 {
		t.Errorf("namespaces leaked into the object list (%d)", namespaceObjects)
	}
}

// A cluster with no Istio types is a real answer, not a failure. The kinds it
// lacks are named so one missing CRD costs its own row rather than the screen.
func TestK8sReaderReportsMissingKinds(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	seed(t, client, gvrNamespaces, u("v1", "Namespace", "", "shop", nil, nil))
	// Every Istio and Gateway API type is absent from this cluster, which is
	// what a mesh-less cluster looks like to the API server.
	for _, res := range []string{"virtualservices", "gateways", "peerauthentications",
		"destinationrules", "serviceentries", "sidecars", "authorizationpolicies",
		"telemetries", "wasmplugins", "httproutes", "grpcroutes"} {
		client.PrependReactor("list", res, func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: res}, "")
		})
	}

	snap := NewK8sReader(context.Background(), client, "istio-system", "role").Snapshot(context.Background())

	if snap.State != StateOK {
		t.Fatalf("state = %q, want ok — namespaces were readable", snap.State)
	}
	if len(snap.Namespaces) != 1 {
		t.Errorf("namespaces = %d, want the one that was readable", len(snap.Namespaces))
	}
	missing := map[string]bool{}
	for _, k := range snap.MissingKinds {
		missing[k] = true
	}
	for _, kind := range []string{KindVirtualService, KindGateway, KindPeerAuthentication} {
		if !missing[kind] {
			t.Errorf("%s absent from the cluster but not reported as missing", kind)
		}
	}
}

// The module is on and the API server refuses us. This is the one state an
// operator can fix, so it must be distinguishable from a mesh-less cluster.
func TestK8sReaderReportsForbidden(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	client.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", nil)
	})

	snap := NewK8sReader(context.Background(), client, "istio-system", "avuruobs-mesh-config").Snapshot(context.Background())

	if snap.State != StateForbidden {
		t.Fatalf("state = %q, want forbidden", snap.State)
	}
	// The fix has to be nameable, or the operator goes looking.
	if !strings.Contains(snap.Reason, "avuruobs-mesh-config") {
		t.Errorf("reason does not name the ClusterRole: %q", snap.Reason)
	}
	if len(snap.Namespaces) != 0 {
		t.Error("a refused read produced namespaces")
	}
}

// managedFields dominate a stored object and nothing here reads them.
func TestStripBulkDropsNoise(t *testing.T) {
	o := u("v1", "Namespace", "", "shop", nil, nil)
	o.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl"}})
	o.SetAnnotations(map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": "{...}",
		"keep-me": "yes",
	})

	got, err := stripBulk(o)
	if err != nil {
		t.Fatalf("stripBulk: %v", err)
	}
	stripped := got.(*unstructured.Unstructured)
	if len(stripped.GetManagedFields()) != 0 {
		t.Error("managedFields survived")
	}
	if _, has := stripped.GetAnnotations()["kubectl.kubernetes.io/last-applied-configuration"]; has {
		t.Error("last-applied-configuration survived")
	}
	if stripped.GetAnnotations()["keep-me"] != "yes" {
		t.Error("an unrelated annotation was dropped")
	}
}
