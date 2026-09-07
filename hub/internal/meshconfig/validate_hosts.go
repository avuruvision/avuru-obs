package meshconfig

import (
	"fmt"
	"strings"
)

// checkVirtualService resolves every host the rule claims.
func checkVirtualService(o Object, idx *index) []Finding {
	var out []Finding
	for _, h := range stringSlice(o.Spec["hosts"]) {
		if f := checkHost(h, o.Namespace, idx); f != nil {
			out = append(out, *f)
		}
	}
	return out
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
		if idx.namespaces[target].MTLSMode == "STRICT" {
			out = append(out, Finding{
				Code:     CodeMTLSConflict,
				Severity: SeverityError,
				Ref:      target,
				RefKind:  RefKindNamespace,
				Message:  fmt.Sprintf("TLS is disabled here while %s requires strict mTLS", target),
				Hint:     "the two disagree and the connection fails — align this rule with the PeerAuthentication, or relax the policy",
			})
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
