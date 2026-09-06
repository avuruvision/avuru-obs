package meshconfig

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var (
	gvrPods     = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	gvrServices = schema.GroupVersionResource{Version: "v1", Resource: "services"}
)

// fullPod is what the API server actually serves: a sidecar-injected,
// Deployment-owned pod with the env, volumes, statuses and annotations that
// make a pod the largest object in a cluster.
func fullPod(namespace, name string) *unstructured.Unstructured {
	env := make([]any, 0, 40)
	for i := range 40 {
		env = append(env, map[string]any{"name": fmt.Sprintf("VAR_%d", i), "value": "a value that adds up"})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         namespace,
			"uid":               "0c2f9b0e-1111-4e1c-9c48-000000000001",
			"resourceVersion":   "12345",
			"creationTimestamp": "2026-09-06T10:00:00Z",
			"labels":            map[string]any{"app": "shop", "pod-template-hash": "abc123"},
			"annotations": map[string]any{
				annotationAmbientRedirection:        "enabled",
				annotationSidecarStatus:             `{"initContainers":["istio-init"],"containers":["istio-proxy"]}`,
				annotationRevision:                  "default",
				"prometheus.io/scrape":              "true",
				"kubectl.kubernetes.io/restartedAt": "2026-09-06T09:00:00Z",
				"checksum/config":                   "deadbeef",
			},
			"ownerReferences": []any{
				map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": name + "-rs", "uid": "x", "controller": true},
			},
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
		},
		"spec": map[string]any{
			"serviceAccountName": "shop-sa",
			"nodeName":           "node-1",
			"hostNetwork":        false,
			"initContainers":     []any{map[string]any{"name": "istio-init", "image": "istio/proxyv2"}},
			"containers": []any{
				map[string]any{"name": "app", "image": "shop:1", "env": env, "ports": []any{map[string]any{"containerPort": int64(8080)}}},
				map[string]any{"name": "istio-proxy", "image": "istio/proxyv2", "env": env},
			},
			"volumes": []any{map[string]any{"name": "istio-envoy", "emptyDir": map[string]any{}}},
		},
		"status": map[string]any{
			"phase":             "Running",
			"podIP":             "10.0.0.7",
			"containerStatuses": []any{map[string]any{"name": "app", "ready": true}},
			"conditions":        []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
}

// The reader keeps pods, projected: what enrolment needs and nothing else, and
// never in the object list where they would drown the configuration.
func TestK8sReaderReadsPodsProjected(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	seed(t, client, gvrPods, fullPod("shop", "shop-7d9f"))

	snap := NewK8sReader(context.Background(), client, "istio-system", "role").Snapshot(context.Background())

	if snap.State != StateOK {
		t.Fatalf("state = %q (%s)", snap.State, snap.Reason)
	}
	if len(snap.Pods) != 1 {
		t.Fatalf("pods = %d, want 1", len(snap.Pods))
	}
	p := snap.Pods[0]
	if p.Namespace != "shop" || p.Name != "shop-7d9f" {
		t.Errorf("pod identity = %s/%s", p.Namespace, p.Name)
	}
	if want := []string{"app", "istio-proxy"}; !reflect.DeepEqual(p.Containers, want) {
		t.Errorf("containers = %v, want %v", p.Containers, want)
	}
	if want := []string{"istio-init"}; !reflect.DeepEqual(p.InitContainers, want) {
		t.Errorf("initContainers = %v, want %v", p.InitContainers, want)
	}
	if p.Annotations[annotationAmbientRedirection] != "enabled" {
		t.Errorf("ambient redirection annotation lost: %v", p.Annotations)
	}
	if p.OwnerKind != "ReplicaSet" || p.OwnerName != "shop-7d9f-rs" {
		t.Errorf("owner = %s/%s", p.OwnerKind, p.OwnerName)
	}
	if p.ServiceAccount != "shop-sa" || p.NodeName != "node-1" || p.Phase != "Running" {
		t.Errorf("pod = %+v", p)
	}
	if p.Labels["app"] != "shop" {
		t.Errorf("labels = %v", p.Labels)
	}
	for _, o := range snap.Objects {
		if o.Kind == KindPod {
			t.Fatal("a pod leaked into the object list")
		}
	}
}

// The projection runs before the informer stores the pod, so the cache holds
// a dozen fields per pod rather than the pod.
func TestProjectPodKeepsOnlyWhatEnrolmentNeeds(t *testing.T) {
	got, err := projectPod(fullPod("shop", "shop-7d9f"))
	if err != nil {
		t.Fatalf("projectPod: %v", err)
	}
	u := got.(*unstructured.Unstructured)

	for _, path := range [][]string{
		{"status", "containerStatuses"},
		{"status", "podIP"},
		{"spec", "volumes"},
		{"metadata", "managedFields"},
		{"metadata", "uid"},
	} {
		if _, has, _ := unstructured.NestedFieldNoCopy(u.Object, path...); has {
			t.Errorf("%v survived the projection", path)
		}
	}
	containers, _, _ := unstructured.NestedSlice(u.Object, "spec", "containers")
	for _, c := range containers {
		if m := c.(map[string]any); len(m) != 1 || m["name"] == nil {
			t.Errorf("a container kept more than its name: %v", m)
		}
	}
	ann := u.GetAnnotations()
	if len(ann) != 3 {
		t.Errorf("annotations = %v, want only the three mesh ones present", ann)
	}
	for _, k := range []string{"prometheus.io/scrape", "checksum/config"} {
		if _, has := ann[k]; has {
			t.Errorf("%s survived the projection", k)
		}
	}
	// The key function still works on the result, or the informer could not
	// store it.
	if u.GetName() != "shop-7d9f" || u.GetNamespace() != "shop" {
		t.Errorf("identity lost: %s/%s", u.GetNamespace(), u.GetName())
	}
	// Idempotent, as the informer contract asks.
	again, _ := projectPod(u.DeepCopy())
	if !reflect.DeepEqual(again.(*unstructured.Unstructured).Object, u.Object) {
		t.Error("projecting twice changed the result")
	}
	// The typed row reads the projection whole.
	p := podFromUnstructured(u)
	if p.OwnerKind != "ReplicaSet" || len(p.Containers) != 2 || p.Annotations[annotationRevision] != "default" {
		t.Errorf("row from projection = %+v", p)
	}
}

// Pods are capped on their own: a cluster with many pods must not cost it its
// configuration, and the cap must say which list it cut.
func TestK8sReaderTruncatesPodsSeparately(t *testing.T) {
	prev := maxPods
	maxPods = 3
	t.Cleanup(func() { maxPods = prev })

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	for i := range maxPods + 1 {
		seed(t, client, gvrPods, fullPod("shop", fmt.Sprintf("shop-%d", i)))
	}
	for _, name := range []string{"cart", "shop", "pay"} {
		seed(t, client, gvrServices, u("v1", "Service", "shop", name, nil, map[string]any{"ports": []any{}}))
	}

	snap := NewK8sReader(context.Background(), client, "istio-system", "role").Snapshot(context.Background())

	if !snap.PodsTruncated {
		t.Error("pods over the cap and PodsTruncated is false")
	}
	if snap.Truncated {
		t.Error("the pod cap was reported as the configuration cap")
	}
	if len(snap.Pods) != maxPods {
		t.Errorf("pods = %d, want the cap (%d)", len(snap.Pods), maxPods)
	}
	if len(snap.Objects) != 3 {
		t.Errorf("objects = %d, want the 3 services untouched", len(snap.Objects))
	}
	for _, k := range snap.Kinds {
		switch k.Kind {
		case KindPod:
			if !k.Truncated || k.Count != maxPods+1 {
				t.Errorf("Pod sync = %+v, want truncated with the full count", k)
			}
		case KindService:
			if k.Truncated {
				t.Error("Service reported truncated")
			}
		}
	}
}
