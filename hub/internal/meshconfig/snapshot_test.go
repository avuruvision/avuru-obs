package meshconfig

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

// clock is a settable time source, so the memo can be tested without waiting
// for it to expire. Locked: the informer's handlers read it from their own
// goroutine.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// countingLister counts cache reads, which is how "the memo was returned" is
// observed rather than inferred.
type countingLister struct {
	cache.GenericLister
	n *atomic.Int32
}

func (c countingLister) List(s labels.Selector) ([]runtime.Object, error) {
	c.n.Add(1)
	return c.GenericLister.List(s)
}

func countReads(r *K8sReader) *atomic.Int32 {
	var n atomic.Int32
	r.mu.Lock()
	for kind, l := range r.listers {
		r.listers[kind] = countingLister{l, &n}
	}
	r.mu.Unlock()
	return &n
}

// eventually polls with a deadline. Never a bare sleep: the informer delivers
// on its own schedule, and the assertion is about what arrives, not when.
func eventually(t *testing.T, want string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", want)
}

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

	c := &clock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	r := newK8sReader(context.Background(), client, "istio-system", "role", c.now)
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
	for _, k := range first.Kinds {
		if (k.Kind == KindGateway) != k.Truncated {
			t.Errorf("%s truncated = %v — only the last-filled kind may be cut", k.Kind, k.Truncated)
		}
	}
	for range 5 {
		c.advance(snapshotTTL) // past the memo: a real rebuild each time
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

// Every readable kind says when its cache warmed; a kind we may not read is
// named as missing, with the reason, and has no sync row to mislead with.
func TestSnapshotKindsCarrySyncTimes(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	seed(t, client, gvrNamespaces, u("v1", "Namespace", "", "shop", nil, nil))
	client.PrependReactor("list", "sidecars", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "sidecars"}, "", nil)
	})

	c := &clock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	snap := newK8sReader(context.Background(), client, "istio-system", "role", c.now).Snapshot(context.Background())

	if snap.State != StateOK {
		t.Fatalf("state = %q (%s)", snap.State, snap.Reason)
	}
	if want := len(watched) - 1; len(snap.Kinds) != want {
		t.Errorf("kinds = %d, want every readable kind (%d)", len(snap.Kinds), want)
	}
	for i, k := range snap.Kinds {
		if k.SyncedAt.IsZero() {
			t.Errorf("%s has no sync time", k.Kind)
		}
		if k.Kind == KindSidecar {
			t.Error("a forbidden kind has a sync row")
		}
		if k.Kind == KindNamespace && (k.Count != 1 || k.LastChangeAt.IsZero()) {
			t.Errorf("Namespace sync = %+v, want one object and a change stamp from the initial list", k)
		}
		if i > 0 && snap.Kinds[i-1].Kind > k.Kind {
			t.Errorf("kinds not sorted at %d: %s > %s", i, snap.Kinds[i-1].Kind, k.Kind)
		}
	}
	if !reflect.DeepEqual(snap.MissingKinds, []string{KindSidecar}) {
		t.Errorf("missing = %v, want just Sidecar", snap.MissingKinds)
	}
	if snap.MissingReasons[KindSidecar] != reasonForbidden {
		t.Errorf("missing reason = %q", snap.MissingReasons[KindSidecar])
	}
}

// Two requests inside the TTL with nothing changed share one snapshot; a
// change — or the TTL — ends that.
func TestSnapshotIsMemoisedUntilSomethingChanges(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds())
	seed(t, client, gvrNamespaces, u("v1", "Namespace", "", "shop", nil, nil))

	c := &clock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	r := newK8sReader(context.Background(), client, "istio-system", "role", c.now)
	reads := countReads(r)
	// The initial list stamped every kind at construction time; step past it
	// so the first snapshot is unambiguously later than every event so far.
	c.advance(time.Millisecond)

	first := r.Snapshot(context.Background())
	after := reads.Load()
	if after == 0 {
		t.Fatal("the first snapshot read no cache")
	}
	c.advance(snapshotTTL / 2)
	second := r.Snapshot(context.Background())
	if reads.Load() != after {
		t.Errorf("a second snapshot inside the TTL re-read the caches (%d -> %d)", after, reads.Load())
	}
	if !reflect.DeepEqual(first.Namespaces, second.Namespaces) {
		t.Error("the memo returned a different snapshot")
	}

	// The TTL alone forces a rebuild.
	c.advance(snapshotTTL)
	r.Snapshot(context.Background())
	if reads.Load() == after {
		t.Error("a snapshot past the TTL did not re-read")
	}

	// An event invalidates the memo before the TTL does.
	before := reads.Load()
	seed(t, client, gvrNamespaces, u("v1", "Namespace", "", "batch", nil, nil))
	eventually(t, "the new namespace to invalidate the memo", func() bool {
		return len(r.Snapshot(context.Background()).Namespaces) == 2
	})
	if reads.Load() == before {
		t.Error("the new namespace appeared without a re-read, which cannot be")
	}
	if fmt.Sprint(r.Snapshot(context.Background()).MissingKinds) != "[]" {
		t.Error("a fully readable cluster reports missing kinds")
	}
}
