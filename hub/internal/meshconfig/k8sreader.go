package meshconfig

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// The caps on one snapshot. A cluster larger than these gets a truncated
// answer that SAYS it is truncated — a short list that does not say so is a lie
// about the cluster, and the screens are unreadable past this size anyway.
//
// Pods have a cap of their own because they are counted in a different unit:
// a cluster with twenty thousand pods has perhaps a few hundred routes, and one
// cap over both would let the pods crowd out the configuration — the half this
// module actually judges. Variables rather than constants so a test can lower
// them without seeding a cluster.
var (
	maxObjects = 5000
	maxPods    = 20000
)

// resyncPeriod is a backstop, not the update path: informers watch. This only
// re-lists in case a watch was silently dropped.
const resyncPeriod = 10 * time.Minute

// warmTimeout bounds how long one cache may take to warm. A cache that never
// warms must not hold the hub's startup hostage.
const warmTimeout = 30 * time.Second

// watched is every resource this product reads, with the Kind it is reported
// under. Unstructured throughout: typed Istio clients would pin the hub to a
// version matrix against whatever Istio the operator runs, and every field we
// extract we extract ourselves.
//
// The ORDER is load-bearing. A snapshot fills in this order and truncates from
// the end, so the kinds validation cannot do without come first and the
// workload kinds — which no check reads yet — are the first to be cut.
var watched = []struct {
	kind string
	gvr  schema.GroupVersionResource
}{
	{KindNamespace, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{KindPod, schema.GroupVersionResource{Version: "v1", Resource: "pods"}},
	{KindService, schema.GroupVersionResource{Version: "v1", Resource: "services"}},
	{KindGateway, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}},
	{KindHTTPRoute, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}},
	{KindGRPCRoute, schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"}},
	{KindVirtualService, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "virtualservices"}},
	{KindDestinationRule, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "destinationrules"}},
	{KindServiceEntry, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "serviceentries"}},
	{KindPeerAuthentication, schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "peerauthentications"}},
	{KindAuthorizationPolicy, schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "authorizationpolicies"}},
	{KindSidecar, schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1", Resource: "sidecars"}},
	{KindTelemetry, schema.GroupVersionResource{Group: "telemetry.istio.io", Version: "v1", Resource: "telemetries"}},
	{KindWasmPlugin, schema.GroupVersionResource{Group: "extensions.istio.io", Version: "v1alpha1", Resource: "wasmplugins"}},
	{"Deployment", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
	{"DaemonSet", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
	{"StatefulSet", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
}

// Why a kind is missing. Three causes, three fixes, so each one is named.
const (
	reasonForbidden = "the ClusterRole does not grant reading it"
	reasonNoType    = "this cluster does not have the type"
	reasonNotWarm   = "its cache did not warm in time"
)

// K8sReader keeps a live view of the cluster's mesh configuration in informer
// caches, and answers Snapshot from them.
type K8sReader struct {
	rootNamespace string
	clusterRole   string
	// now is the clock, injectable so the memo and the sync stamps can be
	// tested without waiting.
	now func() time.Time

	mu       sync.Mutex
	listers  map[string]cache.GenericLister
	missing  map[string]string // kind -> reason
	state    State
	syncedAt time.Time
	// Per kind: when its cache warmed, and when it last saw an event. Exposed
	// on the snapshot so staleness is visible rather than assumed, and used to
	// tell whether the memoised snapshot is still the truth.
	syncedAtByKind map[string]time.Time
	lastChange     map[string]time.Time

	memo   *Snapshot
	memoAt time.Time
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
	return newK8sReader(ctx, client, rootNamespace, clusterRole, time.Now)
}

func newK8sReader(
	ctx context.Context, client dynamic.Interface, rootNamespace, clusterRole string, now func() time.Time,
) *K8sReader {
	r := &K8sReader{
		rootNamespace:  rootNamespace,
		clusterRole:    clusterRole,
		now:            now,
		listers:        map[string]cache.GenericLister{},
		missing:        map[string]string{},
		state:          StateOK,
		syncedAtByKind: map[string]time.Time{},
		lastChange:     map[string]time.Time{},
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(client, resyncPeriod)

	type pending struct {
		kind   string
		synced cache.InformerSynced
	}
	var started []pending
	forbidden := 0
	for _, w := range watched {
		_, err := client.Resource(w.gvr).List(ctx, metav1.ListOptions{Limit: 1})
		switch {
		case err == nil:
			// Reachable: watch it.
		case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
			forbidden++
			r.missing[w.kind] = reasonForbidden
			continue
		case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
			// The cluster does not have this type. A real answer, and only
			// this kind is lost — not the screen.
			r.missing[w.kind] = reasonNoType
			continue
		default:
			r.missing[w.kind] = err.Error()
			continue
		}

		inf := factory.ForResource(w.gvr)
		_ = inf.Informer().SetTransform(transformFor(w.kind))
		// Stamping every event is what lets a snapshot be memoised honestly:
		// the memo is returned only while no kind has changed since it was
		// taken. The registration's HasSynced, rather than the informer's, is
		// what is waited on below — it turns true only once the initial list
		// has been DELIVERED to this handler, so a warm cache never has adds
		// still in flight.
		reg, err := inf.Informer().AddEventHandler(r.stamper(w.kind))
		if err != nil {
			r.missing[w.kind] = err.Error()
			continue
		}
		r.listers[w.kind] = inf.Lister()
		started = append(started, pending{w.kind, reg.HasSynced})
	}

	switch {
	case len(r.listers) == 0 && forbidden > 0:
		r.state = StateForbidden
	case len(r.listers) == 0:
		r.state = StateNoCRDs
	default:
		factory.Start(ctx.Done())
		for _, p := range started {
			warm, cancel := context.WithTimeout(ctx, warmTimeout)
			ok := cache.WaitForCacheSync(warm.Done(), p.synced)
			cancel()
			r.mu.Lock()
			if ok {
				r.syncedAtByKind[p.kind] = r.now()
			} else {
				// A cache that is not warm would answer with part of the
				// cluster and no way to say which part. Missing, and said so.
				delete(r.listers, p.kind)
				r.missing[p.kind] = reasonNotWarm
			}
			r.mu.Unlock()
		}
		r.syncedAt = r.now()
	}
	return r
}

// stamper records that a kind changed. The handler runs on the informer's
// goroutine, so the stamp is the only thing it does.
func (r *K8sReader) stamper(kind string) cache.ResourceEventHandler {
	touch := func() {
		r.mu.Lock()
		r.lastChange[kind] = r.now()
		r.mu.Unlock()
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { touch() },
		UpdateFunc: func(any, any) { touch() },
		DeleteFunc: func(any) { touch() },
	}
}

// transformFor picks what the informer keeps of each object. Pods are
// projected to a dozen fields; everything else only loses its bulk.
func transformFor(kind string) cache.TransformFunc {
	if kind == KindPod {
		return projectPod
	}
	return stripBulk
}

// stripBulk drops the two fields that dominate a stored object and that nothing
// in this product reads: managedFields and last-applied are routinely larger
// than the object itself.
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
