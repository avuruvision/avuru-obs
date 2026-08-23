package health

import (
	"strings"
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
	want, _ := resolve(cfg, stats, labels)
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
		"checkout": {Service: "checkout", Group: "payments", Tier: TierT0, Source: "config", TierSource: "config"},
		"web":      {Service: "web", Group: "storefront", Tier: TierT1, Source: "config", TierSource: "config"},
		"batch":    {Service: "batch", Group: "jobs", Tier: TierT2, Source: "auto", TierSource: "default"},
		"ghost":    {Service: "ghost", Group: unlabeledNamespace, Tier: TierT2, Source: "auto", TierSource: "default"},
	}
	for svc, w := range checks {
		if got[svc] != w {
			t.Errorf("Assign[%q] = %+v, want %+v", svc, got[svc], w)
		}
	}
}

// TestResolveTierPrecedence pins the four-step chain: tierOverrides beats a
// config group, which beats a declared avuru.tier, which beats defaultTier.
func TestResolveTierPrecedence(t *testing.T) {
	cfg := Config{
		DefaultTier:   TierT2,
		Groups:        []Group{{Name: "payments", Tier: TierT1, Selector: Selector{Services: []string{"checkout", "ledger"}}}},
		TierOverrides: map[string]Tier{"checkout": TierT0},
	}
	stats := []storage.ServiceStats{
		{Name: "checkout"}, {Name: "ledger"}, {Name: "web"}, {Name: "batch"},
	}
	labels := []storage.ServiceLabel{
		{Service: "checkout", DeclaredTier: "T3"},                            // override wins
		{Service: "ledger", DeclaredTier: "T3"},                              // config group wins
		{Service: "web", ServiceNamespace: "storefront", DeclaredTier: "T1"}, // declaration wins
		{Service: "batch", ServiceNamespace: "jobs"},                         // default wins
	}

	got, warnings := resolve(cfg, stats, labels)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	want := map[string]struct {
		tier   Tier
		source string
	}{
		"checkout": {TierT0, "override"},
		"ledger":   {TierT1, "config"},
		"web":      {TierT1, "declared"},
		"batch":    {TierT2, "default"},
	}
	for svc, w := range want {
		if got[svc].Tier != w.tier || got[svc].TierSource != w.source {
			t.Errorf("resolve[%q] tier=%q source=%q, want tier=%q source=%q",
				svc, got[svc].Tier, got[svc].TierSource, w.tier, w.source)
		}
	}
}

// TestResolveInvalidDeclaredTierIsSoft: a garbage declared tier falls back to
// defaultTier and produces a warning — never an error, never a crash.
func TestResolveInvalidDeclaredTierIsSoft(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "rogue"}}
	labels := []storage.ServiceLabel{{Service: "rogue", ServiceNamespace: "apps", DeclaredTier: "T9"}}

	got, warnings := resolve(cfg, stats, labels)
	if got["rogue"].Tier != TierT2 {
		t.Errorf("tier = %q, want fallback T2", got["rogue"].Tier)
	}
	if got["rogue"].TierSource != "default" {
		t.Errorf("tierSource = %q, want default", got["rogue"].TierSource)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
	if !strings.Contains(warnings[0], "rogue") || !strings.Contains(warnings[0], "T9") {
		t.Errorf("warning %q should name the service and the bad value", warnings[0])
	}
}

// TestResolveEnvironmentCarried: the declared environment lands on the
// assignment; a service declaring none gets "" (today's behavior).
func TestResolveEnvironmentCarried(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "web"}, {Name: "legacy"}}
	labels := []storage.ServiceLabel{
		{Service: "web", ServiceNamespace: "storefront", Environment: "prod"},
		{Service: "legacy", ServiceNamespace: "storefront"},
	}

	got, _ := resolve(cfg, stats, labels)
	if got["web"].Environment != "prod" {
		t.Errorf("web environment = %q, want prod", got["web"].Environment)
	}
	if got["legacy"].Environment != "" {
		t.Errorf("legacy environment = %q, want empty", got["legacy"].Environment)
	}
}
