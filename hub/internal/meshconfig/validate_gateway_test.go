package meshconfig

import "testing"

func gateway(namespace, name, class string, extra map[string]any) Object {
	spec := map[string]any{"gatewayClassName": class}
	for k, v := range extra {
		spec[k] = v
	}
	return obj(KindGateway, namespace, name, spec)
}

// A Gateway nobody serves — waypoints included — has listeners that exist and
// nothing answering on them.
func TestGatewayNoWorkload(t *testing.T) {
	objects := []Object{
		gateway("amb", "waypoint", "istio-waypoint", nil),
		gateway("amb", "ghost", "istio-waypoint", nil),
		gateway("edge", "public", "istio", nil),
		gateway("edge", "by-hand", "istio", map[string]any{"infrastructure": map[string]any{"labels": map[string]any{"team": "edge"}}}),
	}
	pods := []Pod{
		running("amb", "waypoint-istio-waypoint-x", KindReplicaSet, "waypoint-istio-waypoint", map[string]string{labelGatewayName: "waypoint"}),
		running("edge", "public-istio-x", KindReplicaSet, "public-istio", map[string]string{labelGatewayName: "public"}),
	}
	snap := judge(Snapshot{Objects: objects, Pods: pods})
	for name, want := range map[string]bool{"waypoint": false, "ghost": true, "public": false, "by-hand": false} {
		if got := hasCode(objectFindings(snap, KindGateway, name), CodeGatewayNoWorkload); got != want {
			t.Errorf("%s no-workload = %v, want %v", name, got, want)
		}
	}

	cut := judge(Snapshot{Objects: objects, Pods: pods, PodsTruncated: true})
	if got := codes(cut)[CodeGatewayNoWorkload]; got != 0 {
		t.Errorf("with pods cut: findings = %d, want none", got)
	}
}
