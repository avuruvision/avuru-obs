package green

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
)

// budgetsCfg builds a minimal green config carrying only the given budgets —
// factors are irrelevant to the pure evaluator (it consumes a used-kg map).
func budgetsCfg(budgets ...Budget) Config {
	return Config{Budgets: budgets}
}

var evalNow = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

// notesByRule indexes emitted notifications by their rule key for assertions.
func notesByRule(notes []alerting.Notification) map[string]alerting.Notification {
	out := make(map[string]alerting.Notification, len(notes))
	for _, n := range notes {
		out[n.Rule] = n
	}
	return out
}

// TestEvaluateBudgetsWarnCrossingFiresOnce: ok→warn emits exactly one warn
// notification with the AEP key/target, and the over rule stays silent.
func TestEvaluateBudgetsWarnCrossingFiresOnce(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8, Channel: "ops"})
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 85}, alerting.State{}, evalNow)

	if len(notes) != 1 {
		t.Fatalf("notes = %+v, want exactly one (warn)", notes)
	}
	n := notes[0]
	if n.Rule != "green:web:warn" || n.Target != "group:shop" {
		t.Errorf("notification key/target = %q / %q", n.Rule, n.Target)
	}
	if n.Rule != warnRuleName("web") || n.Target != "group:"+cfg.Budgets[0].Group {
		t.Errorf("helpers disagree with literal: %q %q", warnRuleName("web"), n.Target)
	}
	if n.Kind != alerting.KindFired || n.Status != "warn" || n.Channel != "ops" {
		t.Errorf("notification = %+v, want fired/warn/ops", n)
	}
	warn := next[alerting.StateKey{Rule: "green:web:warn", Target: "group:shop"}]
	if warn.Status != "firing" || !warn.Since.Equal(evalNow) {
		t.Errorf("warn state = %+v, want firing since now", warn)
	}
	if _, over := next[alerting.StateKey{Rule: "green:web:over", Target: "group:shop"}]; over {
		t.Error("over rule must not be in next-state below 100%")
	}
}

// TestEvaluateBudgetsStayingFiringIsSilent: a budget already firing warn emits
// nothing on the next tick (dedup) and keeps its original Since.
func TestEvaluateBudgetsStayingFiringIsSilent(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8})
	since := evalNow.Add(-2 * time.Hour)
	prev := alerting.State{
		{Rule: "green:web:warn", Target: "group:shop"}: {Status: "firing", Since: since, LastNotifiedAt: since},
	}
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 90}, prev, evalNow)

	if len(notes) != 0 {
		t.Fatalf("notes = %+v, want none while staying firing", notes)
	}
	warn := next[alerting.StateKey{Rule: "green:web:warn", Target: "group:shop"}]
	if warn.Status != "firing" || !warn.Since.Equal(since) {
		t.Errorf("warn state = %+v, want firing with preserved Since", warn)
	}
}

// TestEvaluateBudgetsWarnToOver: crossing 100% while already warning fires the
// over rule once; warn stays firing silently.
func TestEvaluateBudgetsWarnToOver(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8, Channel: "ops"})
	prev := alerting.State{
		{Rule: "green:web:warn", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-time.Hour)},
	}
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 105}, prev, evalNow)

	byRule := notesByRule(notes)
	if len(notes) != 1 {
		t.Fatalf("notes = %+v, want exactly one (over)", notes)
	}
	over, ok := byRule["green:web:over"]
	if !ok || over.Kind != alerting.KindFired || over.Status != "exceeded" {
		t.Errorf("over notification = %+v", over)
	}
	if next[alerting.StateKey{Rule: "green:web:warn", Target: "group:shop"}].Status != "firing" {
		t.Error("warn must remain firing")
	}
	if next[alerting.StateKey{Rule: "green:web:over", Target: "group:shop"}].Status != "firing" {
		t.Error("over must be firing")
	}
}

// TestEvaluateBudgetsDropBackResolves: usage falling below warn resolves both
// firing rules and drops them from next-state (so the tick's diffToSave writes
// the explicit ok row).
func TestEvaluateBudgetsDropBackResolves(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8, Channel: "ops"})
	prev := alerting.State{
		{Rule: "green:web:warn", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-time.Hour)},
		{Rule: "green:web:over", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-time.Hour)},
	}
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 50}, prev, evalNow)

	byRule := notesByRule(notes)
	if len(notes) != 2 {
		t.Fatalf("notes = %+v, want two resolves", notes)
	}
	for _, rule := range []string{"green:web:warn", "green:web:over"} {
		n, ok := byRule[rule]
		if !ok || n.Kind != alerting.KindResolved || n.Status != "ok" {
			t.Errorf("%s resolve = %+v", rule, n)
		}
		if _, still := next[alerting.StateKey{Rule: rule, Target: "group:shop"}]; still {
			t.Errorf("%s must be dropped from next after resolve", rule)
		}
	}
}

// TestEvaluateBudgetsOverResolvesToWarn: falling from >100% back into the warn
// band resolves only the over rule; warn stays firing.
func TestEvaluateBudgetsOverResolvesToWarn(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8})
	prev := alerting.State{
		{Rule: "green:web:warn", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-time.Hour)},
		{Rule: "green:web:over", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-time.Hour)},
	}
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 85}, prev, evalNow)

	byRule := notesByRule(notes)
	if len(notes) != 1 {
		t.Fatalf("notes = %+v, want one (over resolve)", notes)
	}
	if n := byRule["green:web:over"]; n.Kind != alerting.KindResolved {
		t.Errorf("over resolve = %+v", n)
	}
	if next[alerting.StateKey{Rule: "green:web:warn", Target: "group:shop"}].Status != "firing" {
		t.Error("warn must remain firing")
	}
	if _, still := next[alerting.StateKey{Rule: "green:web:over", Target: "group:shop"}]; still {
		t.Error("over must be dropped after resolve")
	}
}

// TestEvaluateBudgetsMonthRolloverResolves: month rollover resets month-to-date
// usage to ~0; a firing budget then resolves (the tick recomputes usage, this
// evaluator just sees the lower number).
func TestEvaluateBudgetsMonthRolloverResolves(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8})
	prev := alerting.State{
		{Rule: "green:web:warn", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-48 * time.Hour)},
		{Rule: "green:web:over", Target: "group:shop"}: {Status: "firing", Since: evalNow.Add(-48 * time.Hour)},
	}
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 0}, prev, evalNow)

	if len(notes) != 2 {
		t.Fatalf("notes = %+v, want two resolves on rollover", notes)
	}
	if len(next) != 0 {
		t.Errorf("next = %+v, want empty after both resolve", next)
	}
}

// TestEvaluateBudgetsIndependentAndZeroUsage: budgets are independent, and a
// budget whose group has no usage stays ok (no notes, no state).
func TestEvaluateBudgetsIndependentAndZeroUsage(t *testing.T) {
	cfg := budgetsCfg(
		Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100, WarnRatio: 0.8, Channel: "ops"},
		Budget{Name: "api", Group: "backend", MonthlyKgCO2e: 100, WarnRatio: 0.8, Channel: "ops"},
		Budget{Name: "idle", Group: "empty-team", MonthlyKgCO2e: 100, WarnRatio: 0.8},
	)
	// shop crosses warn; backend and empty-team have no/low usage.
	next, notes := EvaluateBudgets(cfg, map[string]float64{"shop": 85, "backend": 10}, alerting.State{}, evalNow)

	if len(notes) != 1 || notes[0].Rule != "green:web:warn" {
		t.Fatalf("notes = %+v, want only web warn", notes)
	}
	for _, rule := range []string{"green:api:warn", "green:api:over", "green:idle:warn", "green:idle:over"} {
		if _, ok := next[alerting.StateKey{Rule: rule, Target: "group:backend"}]; ok {
			t.Errorf("%s should not fire", rule)
		}
		if _, ok := next[alerting.StateKey{Rule: rule, Target: "group:empty-team"}]; ok {
			t.Errorf("%s should not fire", rule)
		}
	}
	if len(next) != 1 {
		t.Errorf("next = %+v, want only the web warn key", next)
	}
}

// TestEvaluateBudgetsDefaultWarnRatio: a budget that sets no warnRatio uses the
// 80% default (direct-constructed configs skip normalize).
func TestEvaluateBudgetsDefaultWarnRatio(t *testing.T) {
	cfg := budgetsCfg(Budget{Name: "web", Group: "shop", MonthlyKgCO2e: 100}) // WarnRatio 0 → 0.8
	_, below := EvaluateBudgets(cfg, map[string]float64{"shop": 79}, alerting.State{}, evalNow)
	if len(below) != 0 {
		t.Errorf("79%% must not warn under the 0.8 default, got %+v", below)
	}
	_, at := EvaluateBudgets(cfg, map[string]float64{"shop": 80}, alerting.State{}, evalNow)
	if len(at) != 1 || at[0].Rule != "green:web:warn" {
		t.Errorf("80%% must warn under the 0.8 default, got %+v", at)
	}
	if BudgetRulePrefix != "green:" {
		t.Errorf("BudgetRulePrefix = %q, want green:", BudgetRulePrefix)
	}
}
