package meshconfig

import (
	"fmt"
	"strings"
)

// Validate attaches findings to every object in the snapshot.
//
// Pure: a snapshot in, the same snapshot with findings out. No SQL, no HTTP, no
// clock — the same shape as hub/internal/topology, and testable the same way.
//
// Every check below is aimed at breakage that produces SILENCE. That is the
// selection rule, and it is why this is worth reading configuration for at all:
// a route whose backend does not exist drops every request without emitting a
// span, so the half of the product that watches traffic is blindest exactly
// where this failure lives.
//
// Deliberately NOT here, and each for a reason:
//
//   - Style and best-practice checks. Somebody else's product.
//   - Selector-matches-no-workload. Selectors match POD labels, and pods born
//     outside a Deployment/DaemonSet/StatefulSet are invisible to us — a false
//     "this policy protects nothing" is worse than no check at all.
//   - Ambient-enrolled-but-not-behind-ztunnel. It needs the telemetry join,
//     which lives in the API layer; a pure validator has no traffic to consult.
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
	return snap
}

// index is the cross-object lookup every check needs. Built once: the checks
// are O(objects) and a nested scan would be O(objects squared) on a cluster
// where objects is the number the truncation cap exists to bound.
type index struct {
	services     map[string]bool // "namespace/name"
	serviceHosts map[string]bool // every host spelling a Service answers to
	gateways     map[string]bool // "namespace/name"
	waypoints    map[string]bool // "namespace/name", waypoint-class gateways only
	// routedGateways are gateways some route names as a parent.
	routedGateways map[string]bool
	// strictNamespaces have STRICT mTLS, whether from their own policy or the
	// mesh-wide default. NamespacesFrom already resolved that precedence, so
	// this is a lookup rather than a second implementation of the same rule.
	strictNamespaces map[string]bool
}

func newIndex(snap Snapshot) *index {
	idx := &index{
		services:         map[string]bool{},
		serviceHosts:     map[string]bool{},
		gateways:         map[string]bool{},
		waypoints:        map[string]bool{},
		routedGateways:   map[string]bool{},
		strictNamespaces: map[string]bool{},
	}
	for _, o := range snap.Objects {
		switch o.Kind {
		case KindService:
			idx.services[key(o.Namespace, o.Name)] = true
			// A Service answers to several spellings; all of them are valid in
			// a host field, so all of them must resolve.
			idx.serviceHosts[o.Name] = true
			idx.serviceHosts[o.Namespace+"/"+o.Name] = true
			idx.serviceHosts[o.Name+"."+o.Namespace] = true
			idx.serviceHosts[o.Name+"."+o.Namespace+".svc"] = true
			idx.serviceHosts[o.Name+"."+o.Namespace+".svc.cluster.local"] = true
		case KindServiceEntry:
			for _, h := range stringSlice(o.Spec["hosts"]) {
				idx.serviceHosts[h] = true
			}
		case KindGateway:
			idx.gateways[key(o.Namespace, o.Name)] = true
			if IsWaypoint(o) {
				idx.waypoints[key(o.Namespace, o.Name)] = true
			}
		}
	}
	// A second pass: which gateways any route claims as a parent.
	for _, o := range snap.Objects {
		if o.Kind != KindHTTPRoute && o.Kind != KindGRPCRoute {
			continue
		}
		for _, p := range parentRefs(o) {
			idx.routedGateways[p] = true
		}
	}
	for _, ns := range snap.Namespaces {
		if ns.MTLSMode == "STRICT" {
			idx.strictNamespaces[ns.Name] = true
		}
	}
	return idx
}

func key(namespace, name string) string { return namespace + "/" + name }

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
				Message:  fmt.Sprintf("parentRef %s names a Gateway that does not exist", p),
				Hint:     "create the Gateway, or correct the parentRef — until then nothing serves this route",
			})
		}
	}
	for _, rule := range slice(o.Spec["rules"]) {
		r, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		for _, ref := range slice(r["backendRefs"]) {
			b, ok := ref.(map[string]any)
			if !ok {
				continue
			}
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

// checkDestinationRule resolves the host, and catches the mTLS disagreement
// that fails connections without either side looking misconfigured alone.
func checkDestinationRule(o Object, idx *index) []Finding {
	var out []Finding
	host, _ := o.Spec["host"].(string)
	if f := checkHost(host, o.Namespace, idx); f != nil {
		out = append(out, *f)
	}
	if mode := destinationRuleTLSMode(o); mode == "DISABLE" {
		// The namespace it targets, which is the host's namespace when the
		// host names one, and otherwise this rule's own.
		target := o.Namespace
		if _, ns, ok := splitHost(host); ok {
			target = ns
		}
		if idx.strictNamespaces[target] {
			out = append(out, Finding{
				Code:     CodeMTLSConflict,
				Severity: SeverityError,
				Ref:      target,
				Message:  fmt.Sprintf("TLS is disabled here while %s requires strict mTLS", target),
				Hint:     "the two disagree and the connection fails — align this rule with the PeerAuthentication, or relax the policy",
			})
		}
	}
	return out
}

func checkVirtualService(o Object, idx *index) []Finding {
	var out []Finding
	for _, h := range stringSlice(o.Spec["hosts"]) {
		if f := checkHost(h, o.Namespace, idx); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// checkHost resolves one host conservatively.
//
// Conservative on purpose: a host may legitimately name something outside the
// cluster, and a false "this does not exist" on a working external route would
// teach an operator to ignore the whole column. So only cluster-shaped hosts
// are judged — a bare name, or an explicit .svc/.svc.cluster.local — and
// wildcards and external domains are left alone.
func checkHost(host, namespace string, idx *index) *Finding {
	host = strings.TrimSpace(host)
	if host == "" || strings.Contains(host, "*") {
		return nil
	}
	if idx.serviceHosts[host] {
		return nil
	}
	// Qualified with this namespace, for a bare name.
	if !strings.Contains(host, ".") {
		if idx.serviceHosts[host+"."+namespace] || idx.services[key(namespace, host)] {
			return nil
		}
		return unresolved(host)
	}
	if strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local") {
		return unresolved(host)
	}
	// Anything else is plausibly external, and not ours to doubt.
	return nil
}

func unresolved(host string) *Finding {
	return &Finding{
		Code:     CodeHostUnresolved,
		Severity: SeverityWarning,
		Ref:      host,
		Message:  fmt.Sprintf("host %q matches no Service or ServiceEntry", host),
		Hint:     "usually a typo or a deleted service; traffic sent here has no destination",
	}
}

// destinationRuleTLSMode digs spec.trafficPolicy.tls.mode out of a rule.
func destinationRuleTLSMode(o Object) string {
	tp, ok := o.Spec["trafficPolicy"].(map[string]any)
	if !ok {
		return ""
	}
	tls, ok := tp["tls"].(map[string]any)
	if !ok {
		return ""
	}
	mode, _ := tls["mode"].(string)
	return mode
}

// parentRefs returns "namespace/name" for every Gateway a route claims.
func parentRefs(o Object) []string {
	var out []string
	for _, ref := range slice(o.Spec["parentRefs"]) {
		p, ok := ref.(map[string]any)
		if !ok {
			continue
		}
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

// splitHost pulls name and namespace out of a cluster-shaped host.
func splitHost(host string) (name, namespace string, ok bool) {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func slice(v any) []any {
	s, _ := v.([]any)
	return s
}

func stringSlice(v any) []string {
	var out []string
	for _, item := range slice(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
