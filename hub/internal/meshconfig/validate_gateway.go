package meshconfig

import "fmt"

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

// checkListeners reports listeners that cannot coexist: two with one name, or
// two on one port and hostname with different protocols. The gateway is not
// programmed while they conflict, so none of its listeners serve — including
// the ones that were fine.
func checkListeners(o Object) []Finding {
	listeners := mapSlice(o.Spec["listeners"])
	var out []Finding
	for i, a := range listeners {
		for _, b := range listeners[i+1:] {
			nameA, _ := a["name"].(string)
			nameB, _ := b["name"].(string)
			switch {
			case nameA != "" && nameA == nameB:
				out = append(out, listenerConflict(fmt.Sprintf("two listeners are named %q", nameA)))
			case portNumber(a["port"]) == portNumber(b["port"]) && hostnameOf(a) == hostnameOf(b) && protocolOf(a) != protocolOf(b):
				out = append(out, listenerConflict(fmt.Sprintf("listeners %q (%s) and %q (%s) share port %d and hostname %q",
					nameA, protocolOf(a), nameB, protocolOf(b), portNumber(a["port"]), hostnameOf(a))))
			}
		}
	}
	return out
}

func listenerConflict(message string) Finding {
	return Finding{
		Code:     CodeListenerConflict,
		Severity: SeverityError,
		Message:  message,
		Hint:     "the gateway is not programmed while its listeners conflict, so none of them serve",
	}
}

func hostnameOf(l map[string]any) string { h, _ := l["hostname"].(string); return h }
func protocolOf(l map[string]any) string { p, _ := l["protocol"].(string); return p }
