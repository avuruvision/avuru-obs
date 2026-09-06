package meshconfig

import (
	"context"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

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
	sort.Strings(snap.MissingKinds)
	if r.state != StateOK {
		return snap
	}

	// Filled in watched order, so that when the cap is hit the kinds that
	// are cut are the ones declared last — and the same ones every time. Map
	// iteration here would make the cut kind a different one per request.
	var namespaces, peerAuths []Object
	for _, w := range watched {
		lister, ok := r.listers[w.kind]
		if !ok {
			continue
		}
		items := listSorted(lister)
		switch w.kind {
		case KindNamespace:
			for _, u := range items {
				namespaces = append(namespaces, toObject(w.kind, u))
			}
		case KindPod:
			for _, u := range items {
				if len(snap.Pods) >= maxPods {
					snap.PodsTruncated = true
					break
				}
				snap.Pods = append(snap.Pods, podFromUnstructured(u))
			}
		default:
			for _, u := range items {
				o := toObject(w.kind, u)
				// A PeerAuthentication feeds the namespace rows whether or
				// not it fits in the object list: a namespace's mTLS mode
				// must not depend on how many routes the cluster has.
				if w.kind == KindPeerAuthentication {
					peerAuths = append(peerAuths, o)
				}
				if len(snap.Objects) >= maxObjects {
					snap.Truncated = true
					continue
				}
				snap.Objects = append(snap.Objects, o)
			}
		}
	}
	snap.Namespaces = NamespacesFrom(namespaces, peerAuths, r.rootNamespace)

	// Deterministic order: these lists are rendered, diffed and paged, and
	// anything else would reshuffle them on every request.
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
	sortPods(snap.Pods)
	// Judged after the snapshot is whole and ordered: every check is a JOIN
	// across objects, so none of them can run while the set is still being
	// built.
	return Validate(snap)
}

// listSorted reads one cache in namespace/name order. The cache's own order is
// a map's, and a cap that falls inside a kind must cut the same objects on
// every request or the list churns under the reader.
func listSorted(lister cache.GenericLister) []*unstructured.Unstructured {
	items, err := lister.List(labels.Everything())
	if err != nil {
		return nil
	}
	out := make([]*unstructured.Unstructured, 0, len(items))
	for _, item := range items {
		if u, ok := item.(*unstructured.Unstructured); ok {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].GetNamespace(), out[j].GetNamespace(); a != b {
			return a < b
		}
		return out[i].GetName() < out[j].GetName()
	})
	return out
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
