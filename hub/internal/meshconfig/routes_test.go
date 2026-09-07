package meshconfig

import (
	"reflect"
	"testing"
)

// selecting is a Service whose selector picks pods labelled app=<name>.
func selecting(namespace, name string) Object {
	return obj(KindService, namespace, name, map[string]any{
		"selector": map[string]any{"app": name},
	})
}

// A workload's page lists the routes and rules that reach it through its
// Services, spelled however the object spelled the host: a bare name is the
// object's own namespace, a FQDN is exactly that Service, and the same
// object naming the host twice is one reference.
func TestWorkloadRoutesThroughServices(t *testing.T) {
	pods := []Pod{
		running("shop", "checkout-0", KindStatefulSet, "checkout", map[string]string{"app": "checkout"}),
		running("other", "checkout-0", KindStatefulSet, "checkout", map[string]string{"app": "checkout"}),
	}
	objects := []Object{
		selecting("shop", "checkout"), selecting("other", "checkout"),
		route("shop", "web", nil, []any{ref("checkout", nil)}),
		obj(KindGRPCRoute, "shop", "grpc", map[string]any{
			"parentRefs": []any{ref("checkout", map[string]any{"kind": KindService})},
		}),
		obj(KindVirtualService, "shop", "split", map[string]any{
			"hosts": []any{"checkout"},
			"http": []any{map[string]any{"route": []any{
				map[string]any{"destination": map[string]any{"host": "checkout"}},
			}}},
		}),
		obj(KindVirtualService, "shop", "far", map[string]any{
			"hosts": []any{"checkout.other.svc.cluster.local"},
		}),
		obj(KindDestinationRule, "shop", "mtls", map[string]any{"host": "checkout.shop.svc.cluster.local"}),
	}
	snap := judge(Snapshot{Namespaces: []Namespace{{Name: "shop"}, {Name: "other"}}, Pods: pods, Objects: objects})
	got := byWorkload(snap.Workloads)

	want := []RouteRef{
		{Kind: KindDestinationRule, Namespace: "shop", Name: "mtls", Service: "shop/checkout", Host: "checkout.shop.svc.cluster.local"},
		{Kind: KindGRPCRoute, Namespace: "shop", Name: "grpc", Service: "shop/checkout", Host: "checkout"},
		{Kind: KindHTTPRoute, Namespace: "shop", Name: "web", Service: "shop/checkout", Host: "checkout"},
		{Kind: KindVirtualService, Namespace: "shop", Name: "split", Service: "shop/checkout", Host: "checkout"},
	}
	if !reflect.DeepEqual(got["shop/checkout"].Routes, want) {
		t.Errorf("shop/checkout routes = %+v\nwant %+v", got["shop/checkout"].Routes, want)
	}
	wantOther := []RouteRef{
		{Kind: KindVirtualService, Namespace: "shop", Name: "far", Service: "other/checkout", Host: "checkout.other.svc.cluster.local"},
	}
	if !reflect.DeepEqual(got["other/checkout"].Routes, wantOther) {
		t.Errorf("other/checkout routes = %+v\nwant %+v", got["other/checkout"].Routes, wantOther)
	}
}

// A backendRef of another kind is not a Service, and a host nothing answers
// to reaches no workload — neither produces a reference.
func TestWorkloadRoutesIgnoreWhatIsNotAService(t *testing.T) {
	pods := []Pod{running("shop", "checkout-0", KindStatefulSet, "checkout", map[string]string{"app": "checkout"})}
	objects := []Object{
		selecting("shop", "checkout"),
		route("shop", "pool", nil, []any{ref("checkout", map[string]any{"kind": "InferencePool"})}),
		obj(KindVirtualService, "shop", "ghost", map[string]any{"hosts": []any{"nothing"}}),
	}
	snap := judge(Snapshot{Namespaces: []Namespace{{Name: "shop"}}, Pods: pods, Objects: objects})
	if routes := byWorkload(snap.Workloads)["shop/checkout"].Routes; len(routes) != 0 {
		t.Errorf("routes = %+v, want none", routes)
	}
}
