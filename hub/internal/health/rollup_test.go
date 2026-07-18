package health

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const testWindow = time.Minute

func stat(name string, spans, errs uint64, p95ms float64) storage.ServiceStats {
	return storage.ServiceStats{
		Name:       name,
		SpanCount:  spans,
		ErrorCount: errs,
		P95:        time.Duration(p95ms * float64(time.Millisecond)),
	}
}

func label(name, ns string) storage.ServiceLabel {
	return storage.ServiceLabel{Service: name, K8sNamespace: ns}
}

func edge(from, to string) storage.ServiceEdge {
	return storage.ServiceEdge{Source: from, Target: to, Count: 1}
}

// memberOf finds a member across all groups in a report.
func memberOf(r Report, service string) (Member, bool) {
	for _, g := range r.Groups {
		for _, m := range g.Members {
			if m.Service == service {
				return m, true
			}
		}
	}
	return Member{}, false
}

func groupOf(r Report, name string) (GroupHealth, bool) {
	for _, g := range r.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return GroupHealth{}, false
}

func TestServiceStatusRule(t *testing.T) {
	cfg := Default()
	tests := []struct {
		name string
		stat storage.ServiceStats
		want string
	}{
		{"healthy", stat("s", 100, 0, 100), StatusHealthy},
		{"degraded by error", stat("s", 100, 3, 100), StatusDegraded}, // 3% in [1%,5%)
		{"down by error", stat("s", 100, 10, 100), StatusDown},        // 10% >= 5%
		{"degraded by latency", stat("s", 100, 0, 600), StatusDegraded},
		{"idle no traffic", stat("s", 0, 0, 0), StatusIdle},
		{"idle insufficient samples", stat("s", 3, 3, 100), StatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Rollup(cfg, testWindow, []storage.ServiceStats{tt.stat}, nil, nil)
			m, ok := memberOf(r, "s")
			if !ok {
				t.Fatal("service s missing from report")
			}
			if m.BaseStatus != tt.want {
				t.Errorf("BaseStatus = %q, want %q (reason: %s)", m.BaseStatus, tt.want, m.Reason)
			}
		})
	}
}

func TestHybridGrouping(t *testing.T) {
	cfg := Config{
		DefaultTier: TierT2,
		Groups: []Group{
			{Name: "payments", Tier: TierT0, Selector: Selector{Namespaces: []string{"payments"}}},
			{Name: "identity", Tier: TierT0, Selector: Selector{Services: []string{"token-svc"}}},
		},
		Thresholds: ThresholdConfig{Defaults: builtinDefaults},
	}
	stats := []storage.ServiceStats{
		stat("checkout", 100, 0, 100),  // ns payments -> config group payments/T0
		stat("token-svc", 100, 0, 100), // explicit service -> identity/T0
		stat("catalog", 100, 0, 100),   // ns shop, no config -> auto group "shop"/T2
		stat("orphan", 100, 0, 100),    // no label -> auto "(unlabeled)"
	}
	labels := []storage.ServiceLabel{
		label("checkout", "payments"),
		label("token-svc", "identity"),
		label("catalog", "shop"),
	}
	r := Rollup(cfg, testWindow, stats, labels, nil)

	checkM, _ := memberOf(r, "checkout")
	if checkM.Tier != TierT0 {
		t.Errorf("checkout tier = %q, want T0", checkM.Tier)
	}
	if g, ok := groupOf(r, "payments"); !ok || g.Source != "config" {
		t.Errorf("payments group source = %q, want config (found=%v)", g.Source, ok)
	}
	if g, ok := groupOf(r, "identity"); !ok || g.Source != "config" {
		t.Errorf("identity (by service name) missing or not config: %v", ok)
	}
	if g, ok := groupOf(r, "shop"); !ok || g.Source != "auto" || g.Tier != TierT2 {
		t.Errorf("shop auto group wrong: ok=%v source=%q tier=%q", ok, g.Source, g.Tier)
	}
	if _, ok := groupOf(r, unlabeledNamespace); !ok {
		t.Errorf("orphan should land in the %q group", unlabeledNamespace)
	}
}

func TestPropagationCriticalDependencyDown(t *testing.T) {
	cfg := Config{
		DefaultTier: TierT2,
		Groups: []Group{
			{Name: "payments", Tier: TierT0, Selector: Selector{Services: []string{"payments"}}},
		},
		Thresholds: ThresholdConfig{Defaults: builtinDefaults},
	}
	stats := []storage.ServiceStats{
		stat("web", 100, 0, 100),       // healthy on its own, T2
		stat("payments", 100, 20, 100), // 20% errors -> down, T0
		stat("cache", 100, 20, 100),    // down but T2 (not critical)
	}
	edges := []storage.ServiceEdge{edge("web", "payments"), edge("web", "cache")}
	r := Rollup(cfg, testWindow, stats, nil, edges)

	web, _ := memberOf(r, "web")
	if web.BaseStatus != StatusHealthy {
		t.Errorf("web base = %q, want healthy", web.BaseStatus)
	}
	if web.EffectiveStatus != StatusDegraded {
		t.Errorf("web effective = %q, want degraded (critical dep down)", web.EffectiveStatus)
	}
	if web.Reason == "" || web.Reason == "within error budget and latency objective" {
		t.Errorf("web reason should cite the dependency, got %q", web.Reason)
	}
	// The T0 critical dep is listed; the T2 down dep is not (not critical).
	var sawPayments, sawCache bool
	for _, d := range web.Dependencies {
		if d.Service == "payments" {
			sawPayments = true
			if !d.Critical || d.Status != StatusDown {
				t.Errorf("payments dep wrong: critical=%v status=%q", d.Critical, d.Status)
			}
		}
		if d.Service == "cache" {
			sawCache = true
		}
	}
	if !sawPayments {
		t.Error("web should list payments as a critical dependency")
	}
	if sawCache {
		t.Error("cache (T2) must not be a critical dependency")
	}
}

func TestPropagationCycleTerminates(t *testing.T) {
	// a <-> b, both T0; a is down. Reading base (never effective) must make
	// this a single terminating pass.
	cfg := Config{
		DefaultTier: TierT0, // everything T0 -> every edge critical
		Thresholds:  ThresholdConfig{Defaults: builtinDefaults},
	}
	stats := []storage.ServiceStats{
		stat("a", 100, 50, 100), // down
		stat("b", 100, 0, 100),  // healthy
	}
	edges := []storage.ServiceEdge{edge("a", "b"), edge("b", "a")}

	done := make(chan Report, 1)
	go func() { done <- Rollup(cfg, testWindow, stats, nil, edges) }()
	select {
	case r := <-done:
		b, _ := memberOf(r, "b")
		if b.EffectiveStatus != StatusDegraded {
			t.Errorf("b effective = %q, want degraded (depends on down a)", b.EffectiveStatus)
		}
		a, _ := memberOf(r, "a")
		if a.EffectiveStatus != StatusDown {
			t.Errorf("a effective = %q, want down (unchanged; b is healthy)", a.EffectiveStatus)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Rollup did not terminate on a cyclic graph")
	}
}

func TestIdleServiceWithDownDependencyStaysIdle(t *testing.T) {
	cfg := Config{
		DefaultTier: TierT2,
		Groups:      []Group{{Name: "core", Tier: TierT0, Selector: Selector{Services: []string{"db"}}}},
		Thresholds:  ThresholdConfig{Defaults: builtinDefaults},
	}
	stats := []storage.ServiceStats{
		stat("worker", 0, 0, 0),  // idle
		stat("db", 100, 50, 100), // down, T0
	}
	edges := []storage.ServiceEdge{edge("worker", "db")}
	r := Rollup(cfg, testWindow, stats, nil, edges)

	w, _ := memberOf(r, "worker")
	if w.EffectiveStatus != StatusIdle {
		t.Errorf("idle worker must stay idle, got %q", w.EffectiveStatus)
	}
	if w.Reason == "" {
		t.Error("idle worker should still be annotated with the down dependency")
	}
}

func TestGroupRollupAndOverall(t *testing.T) {
	cfg := Config{
		DefaultTier: TierT2,
		Groups: []Group{
			{Name: "t0", Tier: TierT0, Selector: Selector{Namespaces: []string{"crit"}}},
			{Name: "quiet", Tier: TierT2, Selector: Selector{Namespaces: []string{"quiet"}}},
		},
		Thresholds: ThresholdConfig{Defaults: builtinDefaults},
	}
	stats := []storage.ServiceStats{
		stat("a", 100, 0, 100),  // healthy
		stat("b", 100, 50, 100), // down
		stat("z", 0, 0, 0),      // idle
	}
	labels := []storage.ServiceLabel{label("a", "crit"), label("b", "crit"), label("z", "quiet")}
	r := Rollup(cfg, testWindow, stats, labels, nil)

	t0, _ := groupOf(r, "t0")
	if t0.Status != StatusDown {
		t.Errorf("t0 group status = %q, want down (worst-of)", t0.Status)
	}
	if t0.Counts[StatusHealthy] != 1 || t0.Counts[StatusDown] != 1 {
		t.Errorf("t0 counts wrong: %v", t0.Counts)
	}
	quiet, _ := groupOf(r, "quiet")
	if quiet.Status != StatusIdle {
		t.Errorf("all-idle group status = %q, want idle", quiet.Status)
	}
	if r.Overall != StatusDown {
		t.Errorf("overall = %q, want down", r.Overall)
	}
}
