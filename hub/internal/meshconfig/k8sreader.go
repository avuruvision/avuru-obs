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
		_ = inf.Informer().SetTransform(transformFor(w.kind))
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
			warm, cancel := context.WithTimeout(ctx, warmTimeout)
			cache.WaitForCacheSync(warm.Done(), synced)
			cancel()
		}
		r.syncedAt = time.Now()
	}
	return r
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
