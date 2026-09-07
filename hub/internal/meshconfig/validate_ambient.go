package meshconfig

import (
	"fmt"
	"strings"
)

// checkAmbientEnrolment reports the most common ambient misconfiguration: a
// workload labelled for ambient, running, and captured by nobody. Nothing it
// sends or receives goes through the mesh, and nothing it sends or receives
// says so — a workload the node agent skipped produces exactly the traffic it
// produced before the label.
//
// Judged from pods, so it runs only when every pod was read. Two shapes are
// left alone because they are the operator's own choice: a hostNetwork pod,
// which ambient cannot capture, and a pod that opted out with
// istio.io/dataplane-mode=none.
func checkAmbientEnrolment(w Workload, idx *index) []Finding {
	if !idx.podsUsable || w.DeclaredMode != DataplaneAmbient || w.RunningPods == 0 || w.Captured || w.Injected {
		return nil
	}
	for _, p := range idx.podsByWorkload[key(w.Namespace, w.Name)] {
		if p.Phase != phaseRunning {
			continue
		}
		if p.HostNetwork || strings.EqualFold(p.Labels[labelDataplaneMode], valueDataplaneNone) {
			return nil
		}
	}
	return []Finding{{
		Code:     CodeAmbientNotEnrolled,
		Severity: SeverityError,
		Message:  "labelled for ambient, and no pod is captured",
		Hint: "the node agent did not redirect these pods — check the CNI node agent on the pods' nodes and the pod's dataplane labels; " +
			"nothing this workload sends or receives goes through the mesh",
	}}
}

// checkDataplaneConflict reports a workload asked to be two things at once.
// Each shape works, in the sense that traffic flows; each one silently drops
// half of what was configured.
//
// Judged from pods, so it runs only when every pod was read.
func checkDataplaneConflict(w Workload, idx *index) []Finding {
	if !idx.podsUsable {
		return nil
	}
	var out []Finding
	switch {
	case w.Injected && w.Captured:
		out = append(out, Finding{
			Code:     CodeDataplaneConflict,
			Severity: SeverityWarning,
			Message:  "runs a sidecar and is captured by the node agent at once; the sidecar wins and the ambient policies do not apply to it",
			Hint:     "remove the istio.io/dataplane-mode=ambient label from the pods, or the sidecar.istio.io/inject label and the sidecar with it — one data plane, not two",
		})
	case w.Injected && idx.namespaces[w.Namespace].DataplaneMode == DataplaneAmbient:
		out = append(out, Finding{
			Code:     CodeDataplaneConflict,
			Severity: SeverityWarning,
			Message:  "runs a sidecar in an ambient namespace; the sidecar wins and the namespace's ambient policies do not apply to it",
			Hint:     "remove the sidecar.istio.io/inject label and restart the pods, or move the workload out of the ambient namespace",
		})
	}
	for _, p := range idx.podsByWorkload[key(w.Namespace, w.Name)] {
		if strings.EqualFold(p.Labels[labelDataplaneMode], valueAmbient) && p.Labels[labelSidecarInject] == "true" {
			out = append(out, Finding{
				Code:     CodeDataplaneConflict,
				Severity: SeverityWarning,
				Message:  "labelled for ambient and for sidecar injection at once",
				Hint:     "remove one of istio.io/dataplane-mode=ambient and sidecar.istio.io/inject=true from the pod template",
			})
			break
		}
	}
	// Declared, not observed: a workload declared ambient and not captured is
	// checkAmbientEnrolment's finding, and this one would only repeat it.
	if w.Waypoint != "" && w.DeclaredMode != DataplaneAmbient {
		out = append(out, Finding{
			Code:     CodeDataplaneConflict,
			Severity: SeverityWarning,
			Ref:      key(w.WaypointNamespace, w.Waypoint),
			RefKind:  RefKindWaypoint,
			Message:  fmt.Sprintf("bound to waypoint %s but not in ambient — the waypoint is never in the path", key(w.WaypointNamespace, w.Waypoint)),
			Hint:     "enrol the workload in ambient with istio.io/dataplane-mode=ambient, or remove the istio.io/use-waypoint label",
		})
	}
	return out
}
