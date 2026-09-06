package meshconfig

import (
	"context"
	"strings"
	"testing"
)

// A hub outside a cluster must say so. The failure this guards against is
// subtle and bad: reporting an empty OK snapshot would tell an operator their
// mesh has no configuration, which is a confident lie rather than a gap.
func TestNoopReaderSaysWhyItIsEmpty(t *testing.T) {
	snap := NoopReader{}.Snapshot(context.Background())

	if snap.State != StateUnconfigured {
		t.Errorf("state = %q, want unconfigured", snap.State)
	}
	if snap.Reason == "" {
		t.Error("an empty snapshot with no reason is indistinguishable from a mesh with no config")
	}
	if !snap.SyncedAt.IsZero() {
		t.Error("a read that never happened must not carry a timestamp")
	}
	if len(snap.Namespaces) != 0 || len(snap.Objects) != 0 {
		t.Error("the noop reader invented content")
	}
}

// Every state an operator can act on must name the thing to change. This is the
// meshUnavailableReason discipline: three silences, three fixes, and the one
// that sent people to check a working scrape is the reason the rule exists.
func TestEveryActionableStateNamesItsFix(t *testing.T) {
	const role = "avuruobs-mesh-config"

	forbidden := Reason(StateForbidden, role)
	if !strings.Contains(forbidden, role) {
		t.Errorf("the forbidden reason does not name the ClusterRole to grant: %q", forbidden)
	}
	if !strings.Contains(forbidden, "modules.meshConfig.enabled") {
		t.Errorf("the forbidden reason does not name the Helm value: %q", forbidden)
	}
	for _, s := range []State{StateNoCRDs, StateUnconfigured} {
		if Reason(s, role) == "" {
			t.Errorf("state %q has no explanation", s)
		}
	}
	// OK is the one state with nothing to say.
	if Reason(StateOK, role) != "" {
		t.Error("an OK snapshot should carry no reason")
	}
}
