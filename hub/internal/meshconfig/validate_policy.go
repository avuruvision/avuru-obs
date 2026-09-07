package meshconfig

import (
	"fmt"
	"sort"
	"strings"
)

// checkPolicyMatch reports a policy that binds to nothing: a selector no pod
// in its namespace carries, or a targetRef naming an object that is not
// there. A policy protecting nothing reads as protection.
//
// The selector half reads pods and runs only when every pod was read; a
// cut list would make a policy whose pods are past the cap look unmatched.
// The targetRef half reads objects and always runs.
func checkPolicyMatch(o Object, idx *index) []Finding {
	var out []Finding
	for _, ref := range targetRefs(o) {
		var exists bool
		switch ref.kind {
		case KindGateway:
			exists = idx.gateways[ref.id]
		case KindService:
			exists = idx.services[ref.id]
		default:
			// A GatewayClass, a ServiceEntry: kinds this snapshot does not
			// hold whole, and not ours to doubt.
			continue
		}
		if !exists {
			out = append(out, Finding{
				Code:     CodePolicyNoMatch,
				Severity: SeverityWarning,
				Ref:      ref.id,
				RefKind:  ref.kind,
				Message:  fmt.Sprintf("targetRef names %s %s, which does not exist", ref.kind, ref.id),
				Hint:     "a policy that selects nothing protects nothing; create the object or correct the reference",
			})
		}
	}
	sel := selectorLabels(o)
	if len(sel) == 0 || !idx.podsUsable {
		return out
	}
	for _, p := range idx.podsByNS[o.Namespace] {
		if labelsMatch(sel, p.Labels) {
			return out
		}
	}
	return append(out, Finding{
		Code:     CodePolicyNoMatch,
		Severity: SeverityWarning,
		Ref:      o.Namespace,
		RefKind:  RefKindNamespace,
		Message:  fmt.Sprintf("selector %s matches no pod in %s", formatLabels(sel), o.Namespace),
		Hint:     "a policy that selects nothing protects nothing; check the labels against the pods it was meant for",
	})
}

// checkPrincipals reports an authorization rule naming a service account no
// running pod uses. An ALLOW for it allows nobody; a DENY denies nobody —
// and both look, on the page, like a decision that was made.
func checkPrincipals(o Object, idx *index) []Finding {
	if !idx.podsUsable {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for _, rule := range mapSlice(o.Spec["rules"]) {
		for _, from := range mapSlice(rule["from"]) {
			source := nestedMap(from, "source")
			for _, field := range []string{"principals", "notPrincipals"} {
				for _, principal := range stringSlice(source[field]) {
					ns, sa, ok := parsePrincipal(principal)
					id := key(ns, sa)
					if !ok || seen[id] || idx.serviceAccounts[id] {
						continue
					}
					seen[id] = true
					out = append(out, Finding{
						Code:     CodePrincipalUnknown,
						Severity: SeverityWarning,
						Ref:      id,
						RefKind:  RefKindServiceAccount,
						Message:  fmt.Sprintf("principal %q matches no running workload", principal),
						Hint:     "an ALLOW rule for a principal nobody uses allows nobody; a DENY rule for it denies nobody — check the service account",
					})
				}
			}
		}
	}
	return out
}

// parsePrincipal reads namespace and service account out of a principal of
// the form <trust-domain>/ns/<ns>/sa/<sa>, with or without the spiffe://
// prefix. Anything wildcarded, in any position, is not judged: it names a
// set, and this check only knows how to look one account up.
func parsePrincipal(principal string) (namespace, serviceAccount string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(principal, "spiffe://"), "/")
	if len(parts) != 5 || parts[1] != "ns" || parts[3] != "sa" {
		return "", "", false
	}
	if strings.Contains(parts[2], "*") || strings.Contains(parts[4], "*") || parts[2] == "" || parts[4] == "" {
		return "", "", false
	}
	return parts[2], parts[4], true
}

// targetRef is one object a policy is bound to by name.
type targetRef struct {
	kind, id string
}

// targetRefs reads a policy's targetRef and targetRefs, each as kind and
// "namespace/name", the namespace defaulting to the policy's own.
func targetRefs(o Object) []targetRef {
	refs := mapSlice(o.Spec["targetRefs"])
	if one := nestedMap(o.Spec, "targetRef"); one != nil {
		refs = append(refs, one)
	}
	var out []targetRef
	for _, r := range refs {
		kind, _ := r["kind"].(string)
		name, _ := r["name"].(string)
		if kind == "" || name == "" {
			continue
		}
		ns := o.Namespace
		if v, _ := r["namespace"].(string); v != "" {
			ns = v
		}
		out = append(out, targetRef{kind: kind, id: key(ns, name)})
	}
	return out
}

// formatLabels renders a selector as k=v,k=v in key order, so the same
// selector reads the same way every time.
func formatLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
