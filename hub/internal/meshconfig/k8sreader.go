package meshconfig

import (
	"context"
	"sort"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// maxObjects caps one snapshot. A cluster larger than this gets a truncated
// answer that SAYS it is truncated — a short list that does not say so is a lie
// about the cluster, and the screens are unreadable past this size anyway.
const maxObjects = 5000

// resyncPeriod is a backstop, not the update path: informers watch. This only
// re-lists in case a watch was silently dropped.
const resyncPeriod = 10 * time.Minute

// watched is every resource this product reads, with the Kind it is reported
// under. Unstructured throughout: typed Istio clients would pin the hub to a
// version matrix against whatever Istio the operator runs, and every field we
// extract we extract ourselves.
var watched = []struct {
	kind string
	gvr  schema.GroupVersionResource
}{
	{KindNamespace, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{KindService, schema.GroupVersionResource{Version: "v1", Resource: "services"}},
	{"Deployment", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
	{"DaemonSet", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
	{"StatefulSet", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
	{KindGateway, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}},
	{KindHTTPRoute, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}},
	{KindGRPCRoute, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"}},
	{KindVirtualService, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "virtualservices"}},
	{KindDestinationRule, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "destinationrules"}},
	{KindServiceEntry, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "serviceentries"}},
	{KindSidecar, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "sidecars"}},
	{KindPeerAuthentication, schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "peerauthentications"}},
	{KindAuthorizationPolicy, schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "authorizationpolicies"}},
	{KindTelemetry, schema.GroupVersionResource{Group: "telemetry.istio.io", Version: "v1", Resource: "telemetries"}},
	{KindWasmPlugin, schema.GroupVersionResource{Group: "extensions.istio.io", Version: "v1alpha1", Resource: "wasmplugins"}},
}

// K8sReader keeps a live view of the cluster's mesh configuration in informer
// caches, and answers Snapshot from them.
type K8sReader struct {
	rootNamespace string
	clusterRole   string

	mu       sync.RWMutex
	listers  map[string]cache.GenericLister
	missing  []string
	state    State
	syncedAt time.Time
}

// NewK8sReader probes each resource, starts informers for the ones this cluster
// has and this hub may read, and blocks until their caches are warm.
//
// The probe is a one-object List rather than a discovery walk, because it
// answers both questions at once and answers them the way the operator will
// experience them: does this type exist, and are we allowed to read it. A
// discovery call that succeeds tells us nothing about the second.
func NewK8sReader(
	ctx context.Context, client dynamic.Interface, rootNamespace, clusterRole string,
) *K8sReader {
	r := &K8sReader{
		rootNamespace: rootNamespace,
		clusterRole:   clusterRole,
		listers:       map[string]cache.GenericLister{},
		state:         StateOK,
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(client, resyncPeriod)

	var started []cache.InformerSynced
	forbidden := 0
	for _, w := range watched {
		_, err := client.Resource(w.gvr).List(ctx, metav1.ListOptions{Limit: 1})
		switch {
		case err == nil:
			// Reachable: watch it.
		case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
			forbidden++
			r.missing = append(r.missing, w.kind)
			continue
		case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
			// The cluster does not have this type. A real answer, and only
			// this kind is lost — not the screen.
			r.missing = append(r.missing, w.kind)
			continue
		default:
			r.missing = append(r.missing, w.kind)
			continue
		}

		inf := factory.ForResource(w.gvr)
		// managedFields and last-applied are routinely larger than the object
		// itself and nothing here reads them.
		_ = inf.Informer().SetTransform(stripBulk)
		r.listers[w.kind] = inf.Lister()
		started = append(started, inf.Informer().HasSynced)
	}

	switch {
	case len(r.listers) == 0 && forbidden > 0:
		r.state = StateForbidden
	case len(r.listers) == 0:
		r.state = StateNoCRDs
	default:
		factory.Start(ctx.Done())
		for _, synced := range started {
			// Bounded: a cache that never warms must not hold the hub's
			// startup hostage.
			warm, cancel := context.WithTimeout(ctx, 30*time.Second)
			cache.WaitForCacheSync(warm.Done(), synced)
			cancel()
		}
		r.syncedAt = time.Now()
	}
	return r
}

// stripBulk drops the two fields that dominate a stored object and that nothing
// in this product reads.
func stripBulk(i any) (any, error) {
	u, ok := i.(*unstructured.Unstructured)
	if !ok {
		return i, nil
	}
	u.SetManagedFields(nil)
	a := u.GetAnnotations()
	if _, has := a["kubectl.kubernetes.io/last-applied-configuration"]; has {
		delete(a, "kubectl.kubernetes.io/last-applied-configuration")
		u.SetAnnotations(a)
	}
	return u, nil
}

// Snapshot reads the caches. It never calls the API server: that is the point
// of the informers, and it is what lets every screen ask freely.
func (r *K8sReader) Snapshot(context.Context) Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := Snapshot{
		State:        r.state,
		Reason:       Reason(r.state, r.clusterRole),
		SyncedAt:     r.syncedAt,
		MissingKinds: append([]string(nil), r.missing...),
	}
	if r.state != StateOK {
		return snap
	}

	var namespaces, peerAuths []Object
	for kind, lister := range r.listers {
		items, err := lister.List(labels.Everything())
		if err != nil {
			continue
		}
		for _, item := range items {
			u, ok := item.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			o := toObject(kind, u)
			switch kind {
			case KindNamespace:
				namespaces = append(namespaces, o)
			default:
				if len(snap.Objects) >= maxObjects {
					snap.Truncated = true
					continue
				}
				snap.Objects = append(snap.Objects, o)
			}
			if kind == KindPeerAuthentication {
				peerAuths = append(peerAuths, o)
			}
		}
	}
	snap.Namespaces = NamespacesFrom(namespaces, peerAuths, r.rootNamespace)

	// Deterministic order: these lists are rendered, diffed and paged, and map
	// iteration would reshuffle them on every request.
	sort.Slice(snap.Namespaces, func(i, j int) bool { return snap.Namespaces[i].Name < snap.Namespaces[j].Name })
	sort.Slice(snap.Objects, func(i, j int) bool {
		a, b := snap.Objects[i], snap.Objects[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	sort.Strings(snap.MissingKinds)
	return snap
}

func toObject(kind string, u *unstructured.Unstructured) Object {
	spec, _, _ := unstructured.NestedMap(u.Object, "spec")
	return Object{
		Kind:      kind,
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Labels:    u.GetLabels(),
		Spec:      spec,
	}
}
