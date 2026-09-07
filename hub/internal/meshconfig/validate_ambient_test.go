package meshconfig

import (
	"strings"
	"testing"
)

var ambientNS = Namespace{Name: "amb", DataplaneMode: DataplaneAmbient}

// The most common ambient misconfiguration: labelled, running, captured by
// nobody — and producing exactly the traffic it produced before the label.
func TestAmbientNotEnrolled(t *testing.T) {
	hostNet := running("amb", "agent-0", KindDaemonSet, "agent", nil)
	hostNet.HostNetwork = true
	optedOut := running("amb", "legacy-0", KindStatefulSet, "legacy", map[string]string{labelDataplaneMode: "none"})
	pending := running("amb", "new-0", KindStatefulSet, "new", nil)
	pending.Phase = "Pending"
	pods := []Pod{
		running("amb", "cart-0", KindStatefulSet, "cart", nil),
		captured(running("amb", "pay-0", KindStatefulSet, "pay", nil)),
		withSidecar(running("amb", "old-0", KindStatefulSet, "old", nil)),
		hostNet, optedOut, pending,
		// Not an ambient namespace: nothing was asked for.
		running("plain", "web-0", KindStatefulSet, "web", nil),
	}
	snap := judge(Snapshot{Namespaces: []Namespace{ambientNS, {Name: "plain"}}, Pods: pods})

	for id, want := range map[string]bool{
		"amb/cart": true, "amb/pay": false, "amb/old": false, "amb/agent": false,
		"amb/legacy": false, "amb/new": false, "plain/web": false,
	} {
		if got := hasCode(workloadFindings(snap, id), CodeAmbientNotEnrolled); got != want {
			t.Errorf("%s not-enrolled = %v, want %v", id, got, want)
		}
	}
	// The sidecar in the ambient namespace is a conflict, not a missed
	// enrolment: it is in the mesh, just not the way the namespace says.
	if !hasCode(workloadFindings(snap, "amb/old"), CodeDataplaneConflict) {
		t.Error("a sidecar in an ambient namespace was not reported as a data-plane conflict")
	}

	cut := judge(Snapshot{Namespaces: []Namespace{ambientNS}, Pods: pods, PodsTruncated: true})
	if got := codes(cut)[CodeAmbientNotEnrolled]; got != 0 || cut.ChecksSkipped == "" {
		t.Errorf("with pods cut: findings = %d, skipped = %q; want none and a sentence", got, cut.ChecksSkipped)
	}
}

// A workload asked to be two things at once, one finding per shape — and a
// clean ambient workload, bound to its waypoint, has none.
func TestDataplaneConflict(t *testing.T) {
	both := running("amb", "both-0", KindStatefulSet, "both",
		map[string]string{labelDataplaneMode: "ambient", labelSidecarInject: "true"})
	pods := []Pod{
		captured(withSidecar(running("amb", "twice-0", KindStatefulSet, "twice", nil))),
		withSidecar(running("amb", "sidecar-0", KindStatefulSet, "sidecar", nil)),
		captured(both),
		running("plain", "bound-0", KindStatefulSet, "bound", map[string]string{labelUseWaypoint: "wp"}),
		captured(running("amb", "clean-0", KindStatefulSet, "clean", map[string]string{labelUseWaypoint: "wp"})),
	}
	snap := judge(Snapshot{
		Namespaces: []Namespace{ambientNS, {Name: "plain"}},
		Objects:    []Object{obj(KindGateway, "amb", "wp", map[string]any{"gatewayClassName": "istio-waypoint"})},
		Pods:       pods,
	})
	for id, want := range map[string]string{
		"amb/twice":   "runs a sidecar and is captured",
		"amb/sidecar": "runs a sidecar in an ambient namespace",
		"amb/both":    "labelled for ambient and for sidecar injection",
		"plain/bound": "bound to waypoint plain/wp but not in ambient",
	} {
		var found bool
		for _, f := range workloadFindings(snap, id) {
			if f.Code == CodeDataplaneConflict && strings.Contains(f.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no conflict saying %q in %+v", id, want, workloadFindings(snap, id))
		}
	}
	if f := workloadFindings(snap, "amb/clean"); len(f) != 0 {
		t.Errorf("a clean ambient workload has findings: %+v", f)
	}
	// Captured AND injected is one finding, not that one plus the sidecar one.
	var n int
	for _, f := range workloadFindings(snap, "amb/twice") {
		if f.Code == CodeDataplaneConflict {
			n++
		}
	}
	if n != 1 {
		t.Errorf("amb/twice conflicts = %d, want 1", n)
	}
}
