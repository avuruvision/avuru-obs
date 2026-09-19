package meshconfig

import (
	"testing"
	"time"
)

// A narrowed snapshot keeps exactly the named namespaces and everything filed
// under them, and its kind counts describe what is left — a count that still
// says "the cluster has 40 pods" next to a list of 3 is the cluster leaking
// through a number.
func TestNarrowKeepsOnlyNamedNamespaces(t *testing.T) {
	snap := Snapshot{
		State:        StateOK,
		SyncedAt:     time.Now(),
		MissingKinds: []string{KindWasmPlugin},
		Truncated:    true,
		Kinds: []KindSync{
			{Kind: KindNamespace, Count: 3},
			{Kind: KindPod, Count: 3, Truncated: true},
			{Kind: KindService, Count: 2},
		},
		Namespaces: []Namespace{{Name: "istio-system"}, {Name: "other"}, {Name: "shop"}},
		Objects: []Object{
			{Kind: KindService, Namespace: "other", Name: "api"},
			{Kind: KindService, Namespace: "shop", Name: "checkout"},
			{Kind: KindPeerAuthentication, Namespace: "istio-system", Name: "default"},
			{Kind: KindGateway, Name: "cluster-scoped"},
		},
		Pods: []Pod{
			{Namespace: "other", Name: "api-1"},
			{Namespace: "shop", Name: "checkout-1"},
			{Namespace: "shop", Name: "checkout-2"},
		},
		Workloads: []Workload{{Namespace: "other", Name: "api"}, {Namespace: "shop", Name: "checkout"}},
		Services:  []Service{{Namespace: "other", Name: "api"}, {Namespace: "shop", Name: "checkout"}},
	}

	got := snap.Narrow(map[string]bool{"shop": true, "absent": true})

	if len(got.Namespaces) != 1 || got.Namespaces[0].Name != "shop" {
		t.Fatalf("namespaces = %+v, want only shop", got.Namespaces)
	}
	if len(got.Objects) != 1 || got.Objects[0].Name != "checkout" {
		t.Fatalf("objects = %+v, want only shop/checkout", got.Objects)
	}
	if len(got.Pods) != 2 {
		t.Fatalf("pods = %+v, want shop's two", got.Pods)
	}
	if len(got.Workloads) != 1 || got.Workloads[0].Namespace != "shop" {
		t.Fatalf("workloads = %+v, want only shop/checkout", got.Workloads)
	}
	if len(got.Services) != 1 || got.Services[0].Namespace != "shop" {
		t.Fatalf("services = %+v, want only shop/checkout", got.Services)
	}
	counts := map[string]KindSync{}
	for _, k := range got.Kinds {
		counts[k.Kind] = k
	}
	if counts[KindNamespace].Count != 1 || counts[KindPod].Count != 2 || counts[KindService].Count != 1 {
		t.Errorf("kind counts = %+v, want namespace 1, pod 2, service 1", got.Kinds)
	}
	// What was true of the read stays true of the narrowed view.
	if !counts[KindPod].Truncated || !got.Truncated || len(got.MissingKinds) != 1 || got.State != StateOK {
		t.Errorf("read-level facts were dropped: %+v", got)
	}
	// The input is untouched: the reader memoises it and serves other callers.
	if len(snap.Namespaces) != 3 || len(snap.Objects) != 4 || snap.Kinds[0].Count != 3 {
		t.Errorf("Narrow mutated its input: %+v", snap)
	}
}

// A snapshot that was not read has nothing to narrow, and its reason must
// survive — narrowing an error into an empty OK would turn "the cluster was
// not read" into "your project has no namespaces".
func TestNarrowKeepsANonOKSnapshotWhole(t *testing.T) {
	snap := Snapshot{State: StateForbidden, Reason: "no"}
	got := snap.Narrow(map[string]bool{"shop": true})
	if got.State != StateForbidden || got.Reason != "no" {
		t.Fatalf("got %+v", got)
	}
}
