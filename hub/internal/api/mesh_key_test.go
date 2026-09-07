package api

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The sensor's ".namespace" suffix is the one thing that separates a traced
// name from the mesh's own — and only when it IS this namespace. Every other
// dot belongs to the workload.
func TestWorkloadKeyStripsOnlyTheNamespaceSuffix(t *testing.T) {
	for _, tc := range []struct {
		name, ns       string
		wantNS, wantWL string
	}{
		{"global-waypoint.istio-waypoint", "istio-waypoint", "istio-waypoint", "global-waypoint"},
		{"checkout", "shop", "shop", "checkout"},
		// A dotted name that is not the namespace stays whole.
		{"api.v2", "shop", "shop", "api.v2"},
		// The suffix matches a DIFFERENT namespace: not this one, not stripped.
		{"api.v2", "v2-preview", "v2-preview", "api.v2"},
		// A name that is nothing but the suffix is not a workload called "".
		{".shop", "shop", "shop", ".shop"},
		// No namespace known: nothing to strip, and no namespace invented.
		{"global-waypoint.istio-waypoint", "", "", "global-waypoint.istio-waypoint"},
	} {
		t.Run(tc.name+"@"+tc.ns, func(t *testing.T) {
			ns, wl := workloadKey(tc.name, tc.ns)
			if ns != tc.wantNS || wl != tc.wantWL {
				t.Errorf("workloadKey(%q, %q) = (%q, %q), want (%q, %q)",
					tc.name, tc.ns, ns, wl, tc.wantNS, tc.wantWL)
			}
		})
	}
}

// The reverse map is what lets a security row link to the service page. A
// service nobody placed in a namespace cannot be joined and must not be keyed
// under an empty one.
func TestServicesByWorkloadKeysTheMeshWay(t *testing.T) {
	services := []storage.ServiceStats{
		{Name: "global-waypoint.istio-waypoint"},
		{Name: "checkout"},
		{Name: "orphan"},
	}
	namespaces := map[string]string{
		"global-waypoint.istio-waypoint": "istio-waypoint",
		"checkout":                       "shop",
	}
	got := servicesByWorkload(services, namespaces)
	if got[nsWorkloadKey{"istio-waypoint", "global-waypoint"}] != "global-waypoint.istio-waypoint" {
		t.Errorf("waypoint not keyed by its workload name: %v", got)
	}
	if got[nsWorkloadKey{"shop", "checkout"}] != "checkout" {
		t.Errorf("checkout not keyed: %v", got)
	}
	if _, keyed := got[nsWorkloadKey{"", "orphan"}]; keyed {
		t.Error("a service with no namespace was keyed under an empty one")
	}
	if len(got) != 2 {
		t.Errorf("index has %d entries, want 2: %v", len(got), got)
	}
}
