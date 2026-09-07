package meshconfig

import (
	"reflect"
	"testing"
)

func service(namespace, name string, labels map[string]string, selector map[string]any, ports ...map[string]any) Object {
	spec := map[string]any{}
	if selector != nil {
		spec["selector"] = selector
	}
	if len(ports) > 0 {
		items := make([]any, 0, len(ports))
		for _, p := range ports {
			items = append(items, p)
		}
		spec["ports"] = items
	}
	return Object{Kind: KindService, Namespace: namespace, Name: name, Labels: labels, Spec: spec}
}

func byService(ss []Service) map[string]Service {
	out := map[string]Service{}
	for _, s := range ss {
		out[key(s.Namespace, s.Name)] = s
	}
	return out
}

// A selector resolves to workloads, not pods: two pods of one Deployment are
// one entry, and a Service with no selector has none, which is an answer.
func TestServiceSelectorResolvesToWorkloads(t *testing.T) {
	hash := map[string]string{"app": "cart", labelPodTemplateHash: "7d9f"}
	pods := []Pod{
		running("shop", "cart-7d9f-a", KindReplicaSet, "cart-7d9f", hash),
		running("shop", "cart-7d9f-b", KindReplicaSet, "cart-7d9f", hash),
		running("shop", "cart-canary-0", KindStatefulSet, "cart-canary", map[string]string{"app": "cart", "track": "canary"}),
		// Same labels, other namespace: a selector never reaches across.
		running("web", "cart-0", KindStatefulSet, "cart", map[string]string{"app": "cart"}),
	}
	objects := []Object{
		obj(KindDeployment, "shop", "cart", nil),
		service("shop", "cart", nil, map[string]any{"app": "cart"}),
		service("shop", "canary", nil, map[string]any{"track": "canary"}),
		service("shop", "external", nil, nil),
	}
	got := byService(ServicesFrom(objects, pods, nil))
	for id, want := range map[string][]string{
		"shop/cart":     {"shop/cart", "shop/cart-canary"},
		"shop/canary":   {"shop/cart-canary"},
		"shop/external": nil,
	} {
		if !reflect.DeepEqual(got[id].Workloads, want) {
			t.Errorf("%s workloads = %v, want %v", id, got[id].Workloads, want)
		}
	}
	if got["shop/cart"].Selector["app"] != "cart" {
		t.Errorf("selector = %v", got["shop/cart"].Selector)
	}
}

// The protocol a port declares: appProtocol outright, else a name prefix the
// mesh knows, else nothing — and nothing is not "tcp".
func TestServicePortProtocol(t *testing.T) {
	svc := service("shop", "cart", nil, nil,
		map[string]any{"name": "web", "port": int64(80), "protocol": "TCP", "appProtocol": "http"},
		map[string]any{"name": "http-web", "port": int64(8080), "protocol": "TCP"},
		map[string]any{"name": "grpc", "port": float64(9000)},
		map[string]any{"name": "grpc-web-ui", "port": 9001},
		map[string]any{"name": "foo", "port": int64(7000)},
		map[string]any{"port": int64(7001)},
	)
	got := ServicesFrom([]Object{svc}, nil, nil)[0].Ports
	want := []ServicePort{
		{Name: "web", Port: 80, Protocol: "TCP", AppProtocol: "http", DeclaredProtocol: "http"},
		{Name: "http-web", Port: 8080, Protocol: "TCP", DeclaredProtocol: "http"},
		{Name: "grpc", Port: 9000, DeclaredProtocol: "grpc"},
		{Name: "grpc-web-ui", Port: 9001, DeclaredProtocol: "grpc-web"},
		{Name: "foo", Port: 7000},
		{Port: 7001},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ports = %+v\nwant %+v", got, want)
	}
}

// A Service's own waypoint label beats its namespace's, and "none" on the
// Service stops the namespace's binding from reaching it.
func TestServiceWaypointPrecedence(t *testing.T) {
	ns := Namespace{Name: "shop", Waypoint: "shop-waypoint", WaypointNamespace: "shop"}
	objects := []Object{
		service("shop", "a", nil, nil),
		service("shop", "b", map[string]string{labelUseWaypoint: "global", labelWaypointNS: "istio-waypoint"}, nil),
		service("shop", "c", map[string]string{labelUseWaypoint: "none"}, nil),
		service("web", "d", nil, nil),
	}
	got := byService(ServicesFrom(objects, nil, []Namespace{ns}))
	for id, want := range map[string][3]string{
		"shop/a": {"shop-waypoint", "shop", SourceNamespace},
		"shop/b": {"global", "istio-waypoint", SourceService},
		"shop/c": {"", "", SourceService},
		"web/d":  {"", "", ""},
	} {
		s := got[id]
		if got := [3]string{s.Waypoint, s.WaypointNamespace, s.WaypointSource}; got != want {
			t.Errorf("%s = %v, want %v", id, got, want)
		}
	}
}
