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

// checkWaypointBinding reports a binding to a waypoint that is not deployed.
// Called for the object whose own label made the binding — a namespace, or a
// service or workload carrying istio.io/use-waypoint itself — so a namespace
// bound to a missing waypoint is one finding, not one per workload under it.
func checkWaypointBinding(waypoint, waypointNS string, idx *index) []Finding {
	if waypoint == "" || idx.waypoints[key(waypointNS, waypoint)] {
		return nil
	}
	return []Finding{{
		Code:     CodeWaypointMissing,
		Severity: SeverityError,
		Ref:      key(waypointNS, waypoint),
		RefKind:  RefKindWaypoint,
		Message:  fmt.Sprintf("bound to waypoint %s, which is not deployed", key(waypointNS, waypoint)),
		Hint:     "deploy the waypoint or remove the istio.io/use-waypoint label — until then every L7 policy and route for this traffic is silently skipped",
	}}
}

// checkL7WithoutWaypoint reports an object only a waypoint can honour, in an
// ambient namespace where what it targets has none. The L4 data plane cannot
// evaluate it, and what it does instead depends on the kind: an HTTP allow
// rule fails closed, a routing rule is not applied at all.
func checkL7WithoutWaypoint(o Object, idx *index) []Finding {
	if idx.namespaces[o.Namespace].DataplaneMode != DataplaneAmbient || !needsL7(o) || l7TargetsHaveWaypoint(o, idx) {
		return nil
	}
	return []Finding{{
		Code:     CodeL7WithoutWaypoint,
		Severity: SeverityWarning,
		Message:  fmt.Sprintf("%s uses L7 features in an ambient namespace with no waypoint", o.Kind),
		Hint: "the L4 data plane cannot evaluate this — HTTP-level allow rules fail closed and routing rules are not applied; " +
			"bind a waypoint or express the policy in L4 terms",
	}}
}

// needsL7 says whether an object asks for something only a waypoint can do.
func needsL7(o Object) bool {
	switch o.Kind {
	case KindAuthorizationPolicy:
		return authorizationNeedsL7(o)
	case KindRequestAuthentication:
		return true
	case KindVirtualService:
		return len(slice(o.Spec["http"])) > 0 || len(slice(o.Spec["grpc"])) > 0
	case KindDestinationRule:
		tp := nestedMap(o.Spec, "trafficPolicy")
		return nestedMap(tp, "connectionPool", "http") != nil ||
			nestedMap(tp, "outlierDetection") != nil ||
			nestedMap(tp, "loadBalancer", "consistentHash") != nil
	case KindWasmPlugin:
		return len(selectorLabels(o)) > 0
	}
	return false
}

// authorizationNeedsL7 reports whether any rule reads the request rather than
// the connection: methods, paths or hosts, request principals, or a
// condition on a request.* attribute.
func authorizationNeedsL7(o Object) bool {
	for _, rule := range mapSlice(o.Spec["rules"]) {
		for _, to := range mapSlice(rule["to"]) {
			op := nestedMap(to, "operation")
			for _, f := range []string{"methods", "notMethods", "paths", "notPaths", "hosts", "notHosts"} {
				if len(slice(op[f])) > 0 {
					return true
				}
			}
		}
		for _, from := range mapSlice(rule["from"]) {
			src := nestedMap(from, "source")
			if len(slice(src["requestPrincipals"])) > 0 || len(slice(src["notRequestPrincipals"])) > 0 {
				return true
			}
		}
		for _, when := range mapSlice(rule["when"]) {
			if k, _ := when["key"].(string); strings.HasPrefix(k, "request.") {
				return true
			}
		}
	}
	return false
}

// l7TargetsHaveWaypoint resolves what an object applies to — the workloads
// its selector picks, the services behind its hosts, or the object it names
// — and asks whether every one of them is fronted by a waypoint. An object
// bound to a Gateway is on a waypoint or an ingress by definition.
func l7TargetsHaveWaypoint(o Object, idx *index) bool {
	ns := idx.namespaces[o.Namespace]
	switch o.Kind {
	case KindVirtualService:
		return !meshBound(o) || hostsHaveWaypoint(stringSlice(o.Spec["hosts"]), o.Namespace, ns, idx)
	case KindDestinationRule:
		host, _ := o.Spec["host"].(string)
		return hostsHaveWaypoint([]string{host}, o.Namespace, ns, idx)
	}
	if refs := targetRefs(o); len(refs) > 0 {
		for _, r := range refs {
			if s := idx.servicesByKey[r.id]; r.kind == KindService && s != nil && s.Waypoint == "" {
				return false
			}
		}
		return true
	}
	targets := idx.workloadsByNS[o.Namespace]
	if sel := selectorLabels(o); len(sel) > 0 {
		targets = idx.selectedWorkloads(o.Namespace, sel)
	}
	// No workloads known — none running, or the pod list unreadable: the
	// namespace's binding is the best answer left.
	if len(targets) == 0 {
		return ns.Waypoint != ""
	}
	for _, w := range targets {
		if w.Waypoint == "" {
			return false
		}
	}
	return true
}

// hostsHaveWaypoint asks whether every Service the hosts resolve to has a
// waypoint; a host resolving to no Service is answered by the namespace.
func hostsHaveWaypoint(hosts []string, namespace string, ns Namespace, idx *index) bool {
	for _, h := range hosts {
		s := idx.servicesByKey[idx.serviceByHost[idx.hostKey(h, namespace)]]
		if s == nil {
			if ns.Waypoint == "" {
				return false
			}
			continue
		}
		if s.Waypoint == "" {
			return false
		}
	}
	return true
}
