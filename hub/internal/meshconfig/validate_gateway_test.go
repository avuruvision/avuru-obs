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

// Listeners that cannot coexist keep the whole gateway from being programmed
// — including the listeners that were fine.
func TestListenerConflict(t *testing.T) {
	listener := func(name string, port int, hostname, protocol string) map[string]any {
		l := map[string]any{"name": name, "port": int64(port), "protocol": protocol}
		if hostname != "" {
			l["hostname"] = hostname
		}
		return l
	}
	for _, tc := range []struct {
		name      string
		listeners []any
		want      int
	}{
		{"distinct hostnames", []any{listener("a", 443, "a.example.com", "HTTPS"), listener("b", 443, "b.example.com", "TLS")}, 0},
		{"same host and port, same protocol", []any{listener("a", 80, "", "HTTP"), listener("b", 80, "", "HTTP")}, 0},
		{"same host and port, different protocol", []any{listener("a", 443, "x.example.com", "HTTPS"), listener("b", 443, "x.example.com", "TLS")}, 1},
		{"no hostname counts as one hostname", []any{listener("a", 15008, "", "HBONE"), listener("b", 15008, "", "TCP")}, 1},
		{"duplicate names", []any{listener("web", 80, "", "HTTP"), listener("web", 8080, "", "HTTP")}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := Validate(Snapshot{
				MissingKinds: []string{KindPod},
				Objects:      []Object{gateway("edge", "public", "istio", map[string]any{"listeners": tc.listeners})},
			})
			if got := codes(snap)[CodeListenerConflict]; got != tc.want {
				t.Errorf("listener conflicts = %d, want %d: %+v", got, tc.want, objectFindings(snap, KindGateway, "public"))
			}
		})
	}
}
