package meshconfig

// DeclaredMTLS is the PeerAuthentication mode that applies, and where it came
// from.
//
// Three fields because the mode alone answers the wrong question. PERMISSIVE
// on a workload inside a STRICT namespace is not a contradiction, it is a
// selector policy — and the operator reading the row needs that policy by
// name, or the next thing they do is look for a bug in the namespace policy.
type DeclaredMTLS struct {
	// Mode is STRICT, PERMISSIVE or DISABLE, or "" when no policy was read and
	// the mesh default governs. Empty is a real answer: naming a mode we did
	// not read would be a guess.
	Mode string
	// Source is the scope of the policy that decided the mode: "workload",
	// "namespace" or "mesh". Empty when Mode is.
	Source string
	// Policy is "namespace/name" of the PeerAuthentication that decided it.
	Policy string
}

// DeclaredMTLSFor resolves the PeerAuthentication that applies, under the
// mesh's own precedence: a selector policy in the workload's namespace whose
// labels the pod carries, else the namespace-wide policy, else the mesh-wide
// one in rootNamespace, else nothing.
//
// Called with nil podLabels it answers for the namespace itself, which is how
// namespace rows and workload rows are guaranteed to agree: there is one
// resolver, and the namespace is the case with no labels to match.
//
// Two policies at one level — two selector policies both matching a pod, two
// namespace-wide ones — is a state the mesh resolves on its own terms. Here
// the first by name wins and the name is surfaced, so the row says which one
// was assumed instead of hiding that there was a choice.
//
// Port-level modes (portLevelMtls) are not read. A workload's mode is the
// workload's; a per-port exception is a detail this row does not carry, and
// a row that carried it would have to say "STRICT, except" without room for
// the except.
func DeclaredMTLSFor(podLabels map[string]string, namespace string, peerAuths []Object, rootNamespace string) DeclaredMTLS {
	var workload, nsWide, meshWide *Object
	for i := range peerAuths {
		pa := &peerAuths[i]
		if peerAuthMode(*pa) == "" {
			// UNSET: the policy defers to whatever is above it, so it
			// decides nothing and must not shadow the policy that does.
			continue
		}
		sel := selectorLabels(*pa)
		switch {
		case len(sel) > 0:
			if pa.Namespace == namespace && labelsMatch(sel, podLabels) {
				workload = firstByName(workload, pa)
			}
		case pa.Namespace == rootNamespace:
			meshWide = firstByName(meshWide, pa)
		case pa.Namespace == namespace:
			nsWide = firstByName(nsWide, pa)
		}
	}
	switch {
	case workload != nil:
		return decidedBy(*workload, SourceWorkload)
	case nsWide != nil:
		return decidedBy(*nsWide, SourceNamespace)
	case meshWide != nil:
		return decidedBy(*meshWide, SourceMesh)
	}
	return DeclaredMTLS{}
}

func decidedBy(pa Object, source string) DeclaredMTLS {
	return DeclaredMTLS{Mode: peerAuthMode(pa), Source: source, Policy: key(pa.Namespace, pa.Name)}
}

func firstByName(cur, candidate *Object) *Object {
	if cur == nil || candidate.Name < cur.Name {
		return candidate
	}
	return cur
}
