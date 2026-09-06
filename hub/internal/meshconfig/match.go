package meshconfig

// Where a workload's or a namespace's setting came from. Rendered beside the
// value, because a value with no source reads as a contradiction the moment
// two rows disagree.
const (
	SourceWorkload  = "workload"
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
