package health

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestAssignMatchesResolve pins the exported wrapper to the internal
// assignment logic: green must group services exactly as service-health does,
// across config selectors (by service and by namespace), namespace
// auto-grouping, and the unlabeled bucket.
func TestAssignMatchesResolve(t *testing.T) {
	cfg := Config{
		DefaultTier: TierT2,
		Groups: []Group{
			{Name: "payments", Tier: TierT0, Selector: Selector{Services: []string{"checkout"}}},
			{Name: "storefront", Tier: TierT1, Selector: Selector{Namespaces: []string{"shop"}}},
		},
	}
	stats := []storage.ServiceStats{
		{Name: "checkout"}, {Name: "web"}, {Name: "batch"}, {Name: "ghost"},
	}
	labels := []storage.ServiceLabel{
		{Service: "web", K8sNamespace: "shop"},
		{Service: "batch", ServiceNamespace: "jobs"},
	}

	got := Assign(cfg, stats, labels)
	want := resolve(cfg, stats, labels)
	if len(got) != len(want) {
		t.Fatalf("Assign returned %d assignments, resolve %d", len(got), len(want))
	}
	for svc, w := range want {
		if got[svc] != Assignment(w) {
			t.Errorf("Assign[%q] = %+v, resolve gave %+v", svc, got[svc], w)
		}
	}

	// Spot-check the semantics so a shared regression in both paths still
	// fails: selector by name, selector by namespace, auto-group, unlabeled.
	checks := map[string]Assignment{
		"checkout": {Service: "checkout", Group: "payments", Tier: TierT0, Source: "config"},
		"web":      {Service: "web", Group: "storefront", Tier: TierT1, Source: "config"},
		"batch":    {Service: "batch", Group: "jobs", Tier: TierT2, Source: "auto"},
		"ghost":    {Service: "ghost", Group: unlabeledNamespace, Tier: TierT2, Source: "auto"},
	}
	for svc, w := range checks {
		if got[svc] != w {
			t.Errorf("Assign[%q] = %+v, want %+v", svc, got[svc], w)
		}
	}
}
