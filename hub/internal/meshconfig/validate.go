package meshconfig

import (
	"fmt"
	"strings"
)

// Validate attaches findings to every object, namespace, workload and service
// in the snapshot.
//
// Pure: a snapshot in, the same snapshot with findings out. No SQL, no HTTP, no
// clock — the same shape as hub/internal/topology, and testable the same way.
//
// Every check is aimed at breakage that produces SILENCE, or at configuration
// that looks safe and is not. That is the selection rule, and it is why this
// is worth reading configuration for at all: a route whose backend does not
// exist drops every request without emitting a span, so the half of the
// product that watches traffic is blindest exactly where this failure lives.
//
// The checks that read pods — a selector matching nothing, a workload the
// node agent never captured, a Gateway nobody serves, a principal nobody runs
// as — run only when every pod was read. When pods were refused or the list
// was cut they go silent rather than wrong, and ChecksSkipped says so: an
// empty issues column must never be read as a clean bill.
//
// Deliberately NOT here, and each for a reason:
//
//   - Port-naming conventions. This product's spans come from the kernel, not
//     from the proxy's protocol sniffing, so a port named "web" costs it
//     nothing.
//   - "No policy covers this workload". That is the workload's Policies list
//     being empty — a fact on the row, not a finding, because on most
//     clusters it is the state of most workloads.
//   - Style and best-practice checks. Somebody else's product.
//   - ServiceEntry conflicts. Two entries for one host merge under rules that
//     depend on resolution and location; a finding here would be a guess.
func Validate(snap Snapshot) Snapshot {
	idx := newIndex(snap)
	for i := range snap.Objects {
		o := &snap.Objects[i]
		switch o.Kind {
		case KindHTTPRoute, KindGRPCRoute:
			o.Findings = append(o.Findings, checkRoute(*o, idx)...)
		case KindGateway:
			o.Findings = append(o.Findings, checkGateway(*o, idx)...)
		case KindDestinationRule:
			o.Findings = append(o.Findings, checkDestinationRule(*o, idx)...)
		case KindVirtualService:
			o.Findings = append(o.Findings, checkVirtualService(*o, idx)...)
		}
	}
	if !idx.podsUsable {
		snap.ChecksSkipped = checksSkipped(idx.podsWhy)
	}
	return snap
}

// checksSkipped is the one sentence the response carries when the
// pod-dependent checks did not run: which ones, and why.
func checksSkipped(why string) string {
	names := make([]string, 0, len(podGatedChecks))
	for _, c := range podGatedChecks {
		names = append(names, string(c))
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " were skipped: " + why
}

// checkRoute is the highest-value check in the set: a route that attaches,
// matches, and drops every request because its backend is not there.
func checkRoute(o Object, idx *index) []Finding {
	var out []Finding
	for _, p := range parentRefs(o) {
		if !idx.gateways[p] {
			out = append(out, Finding{
				Code:     CodeRouteParentMissing,
				Severity: SeverityError,
				Ref:      p,
				RefKind:  RefKindGateway,
				Message:  fmt.Sprintf("parentRef %s names a Gateway that does not exist", p),
				Hint:     "create the Gateway, or correct the parentRef — until then nothing serves this route",
			})
		}
	}
	for _, r := range mapSlice(o.Spec["rules"]) {
		for _, b := range mapSlice(r["backendRefs"]) {
			name, _ := b["name"].(string)
			if name == "" {
				continue
			}
			// A backendRef may name a kind other than Service (another route,
			// an inference pool). Only Services are ours to resolve.
			if kind, _ := b["kind"].(string); kind != "" && kind != KindService {
				continue
			}
			ns := o.Namespace
			if v, _ := b["namespace"].(string); v != "" {
				ns = v
			}
			if !idx.services[key(ns, name)] {
				out = append(out, Finding{
					Code:     CodeRouteBackendMissing,
					Severity: SeverityError,
					Ref:      key(ns, name),
					RefKind:  RefKindService,
					Message:  fmt.Sprintf("backendRef names Service %s, which does not exist", key(ns, name)),
					Hint:     "every request matching this rule is dropped, and no span is emitted for it — create the Service or fix the reference",
				})
			}
		}
	}
	return out
}

// checkGateway reports a listener nothing attaches to. A warning, not an error:
// a gateway ahead of its routes is a normal moment during a rollout.
func checkGateway(o Object, idx *index) []Finding {
	// A waypoint is addressed by labels, not by parentRefs, so "no routes" is
	// its ordinary state and flagging it would be noise on every ambient
	// install.
	if IsWaypoint(o) {
		return nil
	}
	if idx.routedGateways[key(o.Namespace, o.Name)] {
		return nil
	}
	return []Finding{{
		Code:     CodeGatewayNoRoutes,
		Severity: SeverityWarning,
		Message:  "no route attaches to this Gateway",
		Hint:     "traffic reaching it has nowhere to go — usually a half-finished migration, occasionally the reason an endpoint 404s",
	}}
}

// parentRefs returns "namespace/name" for every Gateway a route claims.
func parentRefs(o Object) []string {
	var out []string
	for _, p := range mapSlice(o.Spec["parentRefs"]) {
		// A route may attach to a Service in ambient mesh mode rather than to a
		// Gateway; only Gateway parents are checked here.
		if kind, _ := p["kind"].(string); kind != "" && kind != KindGateway {
			continue
		}
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		ns := o.Namespace
		if v, _ := p["namespace"].(string); v != "" {
			ns = v
		}
		out = append(out, key(ns, name))
	}
	return out
}
