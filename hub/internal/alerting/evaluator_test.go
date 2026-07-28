package alerting

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

// report builds a health.Report from (group, tier, status) triples, each with
// one same-named member so service: targets exist too.
func report(groups ...[3]string) health.Report {
	r := health.Report{}
	for _, g := range groups {
		name, tier, status := g[0], g[1], g[2]
		r.Groups = append(r.Groups, health.GroupHealth{
			Name:   name,
			Tier:   health.Tier(tier),
			Status: status,
			Reason: name + " is " + status,
			Members: []health.Member{
				{Service: name + "-svc", Tier: health.Tier(tier), EffectiveStatus: status, Reason: "member reason"},
			},
		})
	}
	return r
}

func ruleCfg(when string, forDur time.Duration, sel Selector) Config {
	return Config{
		Channels: []Channel{{Name: "c", Type: "webhook", URL: "https://x"}},
		Rules:    []Rule{{Name: "r", When: when, For: Duration(forDur), Selector: sel, Channel: "c"}},
	}
}

func TestEvaluatePendingToFiringToResolved(t *testing.T) {
	cfg := ruleCfg(WhenDown, 5*time.Minute, Selector{Groups: []string{"payments"}})
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	down := report([3]string{"payments", "T0", health.StatusDown})

	// Tick 1: down -> pending, no notification (for=5m not elapsed).
	st, notes := Evaluate(cfg, down, State{}, t0)
	if len(notes) != 0 {
		t.Fatalf("tick1 should not fire yet: %+v", notes)
	}
	if st[StateKey{"r", "group:payments"}].Status != statePending {
		t.Fatalf("tick1 want pending, got %+v", st)
	}

	// Tick 2: still down, 6m later -> firing + one fired notification.
	st, notes = Evaluate(cfg, down, st, t0.Add(6*time.Minute))
	if len(notes) != 1 || notes[0].Kind != KindFired || notes[0].Target != "group:payments" || notes[0].Channel != "c" {
		t.Fatalf("tick2 want one fired: %+v", notes)
	}
	if st[StateKey{"r", "group:payments"}].Status != stateFiring {
		t.Fatalf("tick2 want firing")
	}

	// Tick 3: still down -> dedup, no notification.
	st, notes = Evaluate(cfg, down, st, t0.Add(7*time.Minute))
	if len(notes) != 0 {
		t.Fatalf("tick3 should dedup: %+v", notes)
	}

	// Tick 4: recovered -> one resolved, key dropped.
	healthy := report([3]string{"payments", "T0", health.StatusHealthy})
	st, notes = Evaluate(cfg, healthy, st, t0.Add(8*time.Minute))
	if len(notes) != 1 || notes[0].Kind != KindResolved {
		t.Fatalf("tick4 want one resolved: %+v", notes)
	}
	if _, ok := st[StateKey{"r", "group:payments"}]; ok {
		t.Fatalf("tick4 key should be dropped after resolve")
	}
}

func TestEvaluatePendingClearsBeforeForNeverFires(t *testing.T) {
	cfg := ruleCfg(WhenDown, 5*time.Minute, Selector{Groups: []string{"payments"}})
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	st, _ := Evaluate(cfg, report([3]string{"payments", "T0", health.StatusDown}), State{}, t0)
	// Recovers 2m later, before the 5m `for`.
	st, notes := Evaluate(cfg, report([3]string{"payments", "T0", health.StatusHealthy}), st, t0.Add(2*time.Minute))
	if len(notes) != 0 {
		t.Fatalf("a pending that clears before `for` must not fire or resolve: %+v", notes)
	}
	if len(st) != 0 {
		t.Fatalf("state should be empty: %+v", st)
	}
}

func TestEvaluateIdleNeverFires(t *testing.T) {
	cfg := ruleCfg(WhenNotHealthy, 0, Selector{Groups: []string{"quiet"}})
	now := time.Now()
	_, notes := Evaluate(cfg, report([3]string{"quiet", "T2", health.StatusIdle}), State{}, now)
	if len(notes) != 0 {
		t.Errorf("idle must never fire: %+v", notes)
	}
}

func TestEvaluateNotHealthyMatchesDegradedAndDown(t *testing.T) {
	cfg := ruleCfg(WhenNotHealthy, 0, Selector{Groups: []string{"g"}}) // for=0 -> fires next tick
	now := time.Now()
	for _, status := range []string{health.StatusDegraded, health.StatusDown} {
		st, _ := Evaluate(cfg, report([3]string{"g", "T1", status}), State{}, now)
		_, notes := Evaluate(cfg, report([3]string{"g", "T1", status}), st, now.Add(time.Second))
		if len(notes) != 1 {
			t.Errorf("not-healthy should fire on %q: %+v", status, notes)
		}
	}
}

func TestEvaluateTierTargetIsWorstOfGroups(t *testing.T) {
	// Two T0 groups: one healthy, one down -> tier:T0 is down.
	cfg := ruleCfg(WhenDown, 0, Selector{Tiers: []string{"T0"}})
	now := time.Now()
	rep := report([3]string{"a", "T0", health.StatusHealthy}, [3]string{"b", "T0", health.StatusDown})
	st, _ := Evaluate(cfg, rep, State{}, now)
	_, notes := Evaluate(cfg, rep, st, now.Add(time.Second))
	if len(notes) != 1 || notes[0].Target != "tier:T0" {
		t.Fatalf("tier:T0 should fire (worst-of down): %+v", notes)
	}
}

func TestEvaluateServiceSelector(t *testing.T) {
	cfg := ruleCfg(WhenDown, 0, Selector{Services: []string{"payments-svc"}})
	now := time.Now()
	rep := report([3]string{"payments", "T0", health.StatusDown})
	st, _ := Evaluate(cfg, rep, State{}, now)
	_, notes := Evaluate(cfg, rep, st, now.Add(time.Second))
	if len(notes) != 1 || notes[0].Target != "service:payments-svc" {
		t.Fatalf("service selector should fire on the member: %+v", notes)
	}
}

func TestEvaluateResolvesWhenTargetVanishes(t *testing.T) {
	cfg := ruleCfg(WhenDown, 0, Selector{Groups: []string{"payments"}})
	now := time.Now()
	st, _ := Evaluate(cfg, report([3]string{"payments", "T0", health.StatusDown}), State{}, now)
	st, notes := Evaluate(cfg, report([3]string{"payments", "T0", health.StatusDown}), st, now.Add(time.Second))
	if len(notes) != 1 {
		t.Fatalf("should be firing")
	}
	// Next tick the group is gone entirely -> resolve.
	_, notes = Evaluate(cfg, health.Report{}, st, now.Add(2*time.Second))
	if len(notes) != 1 || notes[0].Kind != KindResolved {
		t.Fatalf("vanished firing target must resolve: %+v", notes)
	}
}

// TestBuildTargetsPerEnvironment: two environments of one domain must be TWO
// targets. Keying by bare name would let the second overwrite the first and
// silently drop an environment's alerts.
func TestBuildTargetsPerEnvironment(t *testing.T) {
	rep := health.Report{Groups: []health.GroupHealth{
		{Name: "payments", Environment: "prod", Tier: health.TierT0, Status: health.StatusDown, Reason: "prod is down"},
		{Name: "payments", Environment: "staging", Tier: health.TierT2, Status: health.StatusHealthy, Reason: "ok"},
		{Name: "legacy", Status: health.StatusHealthy, Reason: "ok"},
	}}

	got := buildTargets(rep)
	if got["group:payments[prod]"].status != health.StatusDown {
		t.Errorf("group:payments[prod] = %+v, want down", got["group:payments[prod]"])
	}
	if got["group:payments[staging]"].status != health.StatusHealthy {
		t.Errorf("group:payments[staging] = %+v, want healthy", got["group:payments[staging]"])
	}
	// An environment-less group keeps its bare key: existing rules and stored
	// alert state must not be invalidated.
	if got["group:legacy"].status != health.StatusHealthy {
		t.Errorf("group:legacy = %+v, want healthy", got["group:legacy"])
	}
}

// TestSelectorMatchesEnvironmentScopedTarget: a rule naming the domain matches
// every environment of it; adding environments narrows to one.
func TestSelectorMatchesEnvironmentScopedTarget(t *testing.T) {
	all := Selector{Groups: []string{"payments"}}
	for _, target := range []string{"group:payments", "group:payments[prod]", "group:payments[staging]"} {
		if !selectorMatches(all, target) {
			t.Errorf("unnarrowed selector should match %q", target)
		}
	}

	narrowed := Selector{Groups: []string{"payments"}, Environments: []string{"prod"}}
	if !selectorMatches(narrowed, "group:payments[prod]") {
		t.Error("narrowed selector should match its own environment")
	}
	if selectorMatches(narrowed, "group:payments[staging]") {
		t.Error("narrowed selector should not match another environment")
	}
}
