package meshconfig

import (
	"fmt"
	"strings"
)

// checkVirtualService resolves every host the rule claims, the subsets its
// routes send to, and the other rules claiming the same hosts.
func checkVirtualService(o Object, idx *index) []Finding {
	var out []Finding
	for _, h := range stringSlice(o.Spec["hosts"]) {
		if f := checkHost(h, o.Namespace, idx); f != nil {
			out = append(out, *f)
		}
	}
	out = append(out, checkSubsets(o, idx)...)
	if meshBound(o) {
		self := key(o.Namespace, o.Name)
		for _, h := range stringSlice(o.Spec["hosts"]) {
			for _, other := range idx.vsByHost[idx.hostKey(h, o.Namespace)] {
				if other != self {
					out = append(out, hostConflict(KindVirtualService, other, h))
				}
			}
		}
	}
	return out
}

// checkSubsets reports a route to a subset no DestinationRule for that host
// defines. The route matches, and the request has no upstream: it fails at
// the proxy, and the destination never sees it.
func checkSubsets(o Object, idx *index) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, proto := range []string{"http", "tcp", "tls"} {
		for _, rule := range mapSlice(o.Spec[proto]) {
			for _, route := range mapSlice(rule["route"]) {
				dest := nestedMap(route, "destination")
				host, _ := dest["host"].(string)
				subset, _ := dest["subset"].(string)
				k := idx.hostKey(host, o.Namespace)
				// An unresolved host is checkHost's finding; a subset of it
				// would be the same finding twice.
				if subset == "" || k == "" || idx.drSubsetsByHost[k][subset] || seen[k+"#"+subset] {
					continue
				}
				seen[k+"#"+subset] = true
				out = append(out, Finding{
					Code:     CodeSubsetMissing,
					Severity: SeverityError,
					Ref:      host,
					RefKind:  RefKindDestinationRule,
					Message:  fmt.Sprintf("route sends traffic to subset %q of %s, which no DestinationRule defines", subset, host),
					Hint:     "requests to this route get no healthy upstream and the destination never sees them; add the subset or drop it from the route",
				})
			}
		}
	}
	return out
}

// checkDestinationRule resolves the host, catches the mTLS disagreement that
// fails connections without either side looking misconfigured alone, and
// names the other rules claiming the same host.
func checkDestinationRule(o Object, idx *index) []Finding {
	var out []Finding
	host, _ := o.Spec["host"].(string)
	if f := checkHost(host, o.Namespace, idx); f != nil {
		out = append(out, *f)
	}
	out = append(out, checkMTLS(o, host, idx)...)
	self := key(o.Namespace, o.Name)
	for _, other := range idx.drByHost[idx.hostKey(host, o.Namespace)] {
		if other == self {
			continue
		}
		if overlaps(idx.drSubsets[self], idx.drSubsets[other]) || (idx.drTrafficPolicy[self] && idx.drTrafficPolicy[other]) {
			out = append(out, hostConflict(KindDestinationRule, other, host))
		}
	}
	return out
}

// checkMTLS compares what this rule does on the wire with what the
// destination's PeerAuthentication demands, workload by workload: TLS
// disabled toward a workload that requires strict mTLS, or mutual TLS
// demanded of one that disables it. Each workload's own effective policy is
// used — selector-scoped ones included — and only when the host resolves to
// no workload at all does the namespace's answer stand in.
func checkMTLS(o Object, host string, idx *index) []Finding {
	mode := destinationRuleTLSMode(o)
	disables := mode == "DISABLE"
	demands := mode == "ISTIO_MUTUAL" || mode == "MUTUAL"
	if !disables && !demands {
		return nil
	}
	var out []Finding
	if workloads := idx.workloadsBehind(host, o.Namespace); len(workloads) > 0 {
		for _, w := range workloads {
			declared := w.DeclaredMTLS.Mode
			switch {
			case disables && declared == "STRICT":
				out = append(out, mtlsConflict(key(w.Namespace, w.Name), RefKindWorkload,
					fmt.Sprintf("TLS is disabled here while %s requires strict mTLS (%s)", key(w.Namespace, w.Name), w.DeclaredMTLS.Policy)))
			case demands && declared == "DISABLE":
				out = append(out, mtlsConflict(key(w.Namespace, w.Name), RefKindWorkload,
					fmt.Sprintf("mutual TLS is demanded here while %s disables it (%s)", key(w.Namespace, w.Name), w.DeclaredMTLS.Policy)))
			}
		}
		return out
	}
	// The namespace it targets, which is the host's namespace when the host
	// names one, and otherwise this rule's own.
	target := o.Namespace
	if _, ns, ok := splitHost(host); ok {
		target = ns
	}
	switch declared := idx.namespaces[target].MTLSMode; {
	case disables && declared == "STRICT":
		out = append(out, mtlsConflict(target, RefKindNamespace, fmt.Sprintf("TLS is disabled here while %s requires strict mTLS", target)))
	case demands && declared == "DISABLE":
		out = append(out, mtlsConflict(target, RefKindNamespace, fmt.Sprintf("mutual TLS is demanded here while %s disables it", target)))
	}
	return out
}

func mtlsConflict(ref, refKind, message string) Finding {
	return Finding{
		Code:     CodeMTLSConflict,
		Severity: SeverityError,
		Ref:      ref,
		RefKind:  refKind,
		Message:  message,
		Hint:     "the two disagree and the connection fails — align this rule with the PeerAuthentication, or change the policy",
	}
}

func hostConflict(kind, other, host string) Finding {
	refKind := RefKindVirtualService
	if kind == KindDestinationRule {
		refKind = RefKindDestinationRule
	}
	return Finding{
		Code:     CodeHostConflict,
		Severity: SeverityWarning,
		Ref:      other,
		RefKind:  refKind,
		Message:  fmt.Sprintf("another %s (%s) also claims host %s; only one is applied", kind, other, host),
		Hint:     "merge them, or scope one with exportTo — the ignored one looks configured and does nothing",
	}
}

func overlaps(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
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
	if idx.hostKey(host, namespace) != "" {
		return nil
	}
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local") {
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
		RefKind:  RefKindHost,
		Message:  fmt.Sprintf("host %q matches no Service or ServiceEntry", host),
		Hint:     "usually a typo or a deleted service; traffic sent here has no destination",
	}
}

// destinationRuleTLSMode digs spec.trafficPolicy.tls.mode out of a rule.
func destinationRuleTLSMode(o Object) string {
	mode, _ := nestedMap(o.Spec, "trafficPolicy", "tls")["mode"].(string)
	return mode
}
