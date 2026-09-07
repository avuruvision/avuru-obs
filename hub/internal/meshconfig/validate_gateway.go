package meshconfig

// checkGatewayWorkload reports a Gateway — waypoints included — that no
// running pod serves. Its listeners exist, its routes attach, and nothing
// answers: the failure looks like a slow rollout from every side but the
// pods'.
//
// Judged from pods, so it runs only when every pod was read. A Gateway with
// spec.infrastructure set is deployed by hand, under a name this check cannot
// guess, and is left alone.
func checkGatewayWorkload(o Object, idx *index) []Finding {
	if !idx.podsUsable || idx.gatewayPods[key(o.Namespace, o.Name)] > 0 {
		return nil
	}
	if _, manual := o.Spec["infrastructure"]; manual {
		return nil
	}
	return []Finding{{
		Code:     CodeGatewayNoWorkload,
		Severity: SeverityError,
		Message:  "no running pod serves this Gateway",
		Hint:     "the listeners exist and nothing answers on them; check the gateway deployment in this namespace",
	}}
}
