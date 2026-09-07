package meshconfig

import (
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The pod annotations the mesh writes, and the only ones the reader keeps.
const (
	annotationAmbientRedirection = "ambient.istio.io/redirection"
	annotationSidecarInject      = "sidecar.istio.io/inject"
	annotationSidecarStatus      = "sidecar.istio.io/status"
	annotationRevision           = "istio.io/rev"
)

var keptPodAnnotations = []string{
	annotationAmbientRedirection,
	annotationSidecarInject,
	annotationSidecarStatus,
	annotationRevision,
}

// projectPod is the pod informer's transform: it rewrites a pod to the dozen
// fields enrolment needs BEFORE the informer stores it, so what the cache holds
// per pod is a projection rather than a pod.
//
// This is the difference between a bounded reader and one that holds every
// container's env, every volume and every condition of every pod in the cluster
// — the object a pod actually is, and the largest thing an API server serves.
// Nothing here reads any of that, so none of it is kept.
func projectPod(i any) (any, error) {
	u, ok := i.(*unstructured.Unstructured)
	if !ok {
		return i, nil
	}
	meta := map[string]any{
		"name":      u.GetName(),
		"namespace": u.GetNamespace(),
	}
	if labels := u.GetLabels(); len(labels) > 0 {
		meta["labels"] = toAnyMap(labels)
	}
	if kept := keepAnnotations(u.GetAnnotations()); len(kept) > 0 {
		meta["annotations"] = toAnyMap(kept)
	}
	if owners := projectOwners(u); len(owners) > 0 {
		meta["ownerReferences"] = owners
	}
	if ts, ok, _ := unstructured.NestedString(u.Object, "metadata", "creationTimestamp"); ok {
		meta["creationTimestamp"] = ts
	}

	spec := map[string]any{}
	for _, field := range []string{"serviceAccountName", "nodeName"} {
		if v, ok, _ := unstructured.NestedString(u.Object, "spec", field); ok {
			spec[field] = v
		}
	}
	if v, ok, _ := unstructured.NestedBool(u.Object, "spec", "hostNetwork"); ok {
		spec["hostNetwork"] = v
	}
	for _, field := range []string{"containers", "initContainers"} {
		if names := containerNames(u, field); len(names) > 0 {
			spec[field] = names
		}
	}

	out := map[string]any{
		"apiVersion": u.GetAPIVersion(),
		"kind":       u.GetKind(),
		"metadata":   meta,
		"spec":       spec,
	}
	if phase, ok, _ := unstructured.NestedString(u.Object, "status", "phase"); ok {
		out["status"] = map[string]any{"phase": phase}
	}
	u.Object = out
	return u, nil
}

// keepAnnotations returns only the mesh's own annotations.
func keepAnnotations(all map[string]string) map[string]string {
	var kept map[string]string
	for _, k := range keptPodAnnotations {
		if v, has := all[k]; has {
			if kept == nil {
				kept = map[string]string{}
			}
			kept[k] = v
		}
	}
	return kept
}

// projectOwners keeps kind, name and the controller flag of each owner — enough
// to join a pod to its workload, and none of the uids or apiVersions.
func projectOwners(u *unstructured.Unstructured) []any {
	var out []any
	for _, o := range u.GetOwnerReferences() {
		ref := map[string]any{"kind": o.Kind, "name": o.Name}
		if o.Controller != nil {
			ref["controller"] = *o.Controller
		}
		out = append(out, ref)
	}
	return out
}

// containerNames reduces spec.<field> to [{name}] entries.
func containerNames(u *unstructured.Unstructured, field string) []any {
	items, _, _ := unstructured.NestedSlice(u.Object, "spec", field)
	var out []any
	for _, item := range items {
		c, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := c["name"].(string); name != "" {
			out = append(out, map[string]any{"name": name})
		}
	}
	return out
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// podFromUnstructured reads a projected pod into its typed row. It is written
// against the projection, so it tolerates a whole pod too — it just ignores the
// rest.
func podFromUnstructured(u *unstructured.Unstructured) Pod {
	p := Pod{
		Namespace:   u.GetNamespace(),
		Name:        u.GetName(),
		Labels:      u.GetLabels(),
		Annotations: keepAnnotations(u.GetAnnotations()),
	}
	for _, o := range u.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller {
			p.OwnerKind, p.OwnerName = o.Kind, o.Name
			break
		}
	}
	p.ServiceAccount, _, _ = unstructured.NestedString(u.Object, "spec", "serviceAccountName")
	p.NodeName, _, _ = unstructured.NestedString(u.Object, "spec", "nodeName")
	p.HostNetwork, _, _ = unstructured.NestedBool(u.Object, "spec", "hostNetwork")
	p.Containers = namesOf(u, "containers")
	p.InitContainers = namesOf(u, "initContainers")
	p.Phase, _, _ = unstructured.NestedString(u.Object, "status", "phase")
	return p
}

func namesOf(u *unstructured.Unstructured, field string) []string {
	var out []string
	for _, item := range containerNames(u, field) {
		c := item.(map[string]any)
		out = append(out, c["name"].(string))
	}
	return out
}

// sortPods orders pods by namespace then name, so a paged list is stable across
// requests and the cut, when there is one, falls in the same place each time.
func sortPods(pods []Pod) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
}
