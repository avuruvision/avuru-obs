package meshconfig

import "strings"

// Labels and names this file joins on. A pod's owner and a policy's target are
// both spelled in labels, and this is where that spelling is read.
const (
	labelPodTemplateHash = "pod-template-hash"
	labelSidecarInject   = "sidecar.istio.io/inject"
	containerIstioProxy  = "istio-proxy"
	phaseRunning         = "Running"
	valueDataplaneNone   = "none"
)

// Where a workload's, a service's or a namespace's setting came from.
// Rendered beside the value, because a value with no source reads as a
// contradiction the moment two rows disagree.
const (
	SourceWorkload  = "workload"
	SourceService   = "service"
	SourceNamespace = "namespace"
	SourceMesh      = "mesh"
)

// selectorLabels returns the labels a policy selects workloads by, or nil when
// it applies to its whole scope.
//
// The mesh spells it two ways: spec.selector.matchLabels on security,
// telemetry and extension policies, spec.workloadSelector.labels on a Sidecar.
// Both mean the same thing and both are read here, so no caller has to know
// which kind it is holding.
func selectorLabels(o Object) map[string]string {
	for _, path := range [][]string{{"selector", "matchLabels"}, {"workloadSelector", "labels"}} {
		if m := nestedStringMap(o.Spec, path...); len(m) > 0 {
			return m
		}
	}
	return nil
}

// hasTargetRef reports whether a policy is bound to a named object — a
// Gateway, or a Service on a waypoint — rather than to workloads by label.
// Such a policy belongs to the object it names and attaches to no workload
// here; attributing it to the namespace's workloads would list protection
// they do not have.
func hasTargetRef(o Object) bool {
	if _, one := o.Spec["targetRef"]; one {
		return true
	}
	return len(slice(o.Spec["targetRefs"])) > 0
}

// labelsMatch reports whether every selector label is on the pod. An empty
// selector matches nothing here on purpose: callers treat "no selector" as
// scope-wide before they get this far, and a nil label set must never match a
// selector that asked for something.
func labelsMatch(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// workloadKey identifies the thing that made a pod.
type workloadKey struct {
	Namespace, Name, Kind string
}

func (k workloadKey) id() string { return key(k.Namespace, k.Name) }

// workloadKeyFor joins a pod to its workload without a ReplicaSet watch.
//
// A Deployment's ReplicaSet is named <deployment>-<pod-template-hash>, and the
// hash is also a label on every pod it makes; when the Deployment that name
// implies exists, the pod is the Deployment's. When it cannot be confirmed —
// the Deployment list was cut, or the ReplicaSet is a bare one — the pod is
// reported as its ReplicaSet's, which is true rather than a guess. Any other
// controller is named as itself, and a pod with no controller is its own
// workload.
func workloadKeyFor(p Pod, deployments map[string]bool) workloadKey {
	switch p.OwnerKind {
	case "":
		return workloadKey{p.Namespace, p.Name, KindPod}
	case KindReplicaSet:
		hash := p.Labels[labelPodTemplateHash]
		if hash != "" && strings.HasSuffix(p.OwnerName, "-"+hash) {
			name := strings.TrimSuffix(p.OwnerName, "-"+hash)
			if deployments[key(p.Namespace, name)] {
				return workloadKey{p.Namespace, name, KindDeployment}
			}
		}
		return workloadKey{p.Namespace, p.OwnerName, KindReplicaSet}
	default:
		return workloadKey{p.Namespace, p.OwnerName, p.OwnerKind}
	}
}

// deploymentSet lists every Deployment read, as "namespace/name", which is
// what confirming a template hash needs.
func deploymentSet(objects []Object) map[string]bool {
	out := map[string]bool{}
	for _, o := range objects {
		if o.Kind == KindDeployment {
			out[key(o.Namespace, o.Name)] = true
		}
	}
	return out
}

func nestedStringMap(m map[string]any, path ...string) map[string]string {
	var cur any = m
	for _, p := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[p]
	}
	raw, ok := cur.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
