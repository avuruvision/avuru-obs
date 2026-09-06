package meshconfig

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// When the cap is hit, the kind that is cut is the LAST one in watched order,
// and it is the same kind — and the same objects — on every request. The old
// reader iterated a map, so the cut kind was random per request.
func TestSnapshotTruncationIsDeterministic(t *testing.T) {
	prev := maxObjects
	maxObjects = 4
	t.Cleanup(func() { maxObjects = prev })

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	for _, name := range []string{"cart", "shop", "pay"} {
		seed(t, client, gvrServices, u("v1", "Service", "shop", name, nil, map[string]any{"ports": []any{}}))
	}
	for _, name := range []string{"edge", "admin"} {
		seed(t, client, gvrGateways, u("gateway.networking.k8s.io/v1", "Gateway", "shop", name, nil,
			map[string]any{"gatewayClassName": "istio"}))
	}

	r := NewK8sReader(context.Background(), client, "istio-system", "role")
	first := r.Snapshot(context.Background())

	if !first.Truncated {
		t.Fatal("five objects over a cap of four and Truncated is false")
	}
	var kinds []string
	for _, o := range first.Objects {
		kinds = append(kinds, o.Kind+"/"+o.Name)
	}
	// Services fill first, being earlier in watched order; the gateway list is
	// where the cut falls, and within it the first by name survives.
	want := []string{"Gateway/admin", "Service/cart", "Service/pay", "Service/shop"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("objects = %v, want %v", kinds, want)
	}
	for range 5 {
		again := r.Snapshot(context.Background())
		var got []string
		for _, o := range again.Objects {
			got = append(got, o.Kind+"/"+o.Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("a rebuild cut differently: %v", got)
		}
	}
}
