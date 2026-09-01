package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/alerting"
)

func at(min int) time.Time {
	return time.Date(2026, 9, 1, 12, min, 0, 0, time.UTC)
}

func tokenBudget(limit int64) Config {
	return Config{Budgets: []Budget{{Name: "estate", MonthlyTokens: limit}}}
}

func usageTokens(n int64) BudgetUsage {
	return BudgetUsage{EstateTokens: n}
}

func statuses(notes []alerting.Notification) map[string]string {
	out := map[string]string{}
	for _, n := range notes {
		out[n.Rule] = n.Status
	}
	return out
}

// The four crossings, over the pure machine and with no database — the same
// shape green's budget tests take, so the two stay comparable.
func TestEvaluateBudgetsCrossings(t *testing.T) {
	cases := []struct {
		name       string
		used       int64
		wantFired  []string
		wantStates []string
	}{
		{"well under fires nothing", 100, nil, nil},
		{"crossing warn fires warn only", 850, []string{"ai:estate:warn"}, []string{"ai:estate:warn"}},
		{"crossing the budget fires both", 1200,
			[]string{"ai:estate:warn", "ai:estate:over"},
			[]string{"ai:estate:warn", "ai:estate:over"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, notes := EvaluateBudgets(tokenBudget(1000), usageTokens(tc.used), alerting.State{}, at(0))
			if len(notes) != len(tc.wantFired) {
				t.Fatalf("notes = %d (%+v), want %d", len(notes), notes, len(tc.wantFired))
			}
			got := statuses(notes)
			for _, rule := range tc.wantFired {
				if _, ok := got[rule]; !ok {
					t.Errorf("%s did not fire: %+v", rule, notes)
				}
			}
			if len(next) != len(tc.wantStates) {
				t.Errorf("next = %d keys (%+v), want %d", len(next), next, len(tc.wantStates))
			}
		})
	}
}

// A rule fires once. The second tick over the same crossing must not re-notify,
// or a budget that stays over sends an alert every tick forever.
func TestEvaluateBudgetsDedupsWhileFiring(t *testing.T) {
	cfg := tokenBudget(1000)
	next, notes := EvaluateBudgets(cfg, usageTokens(1200), alerting.State{}, at(0))
	if len(notes) != 2 {
		t.Fatalf("first tick notes = %d, want 2", len(notes))
	}
	_, notes2 := EvaluateBudgets(cfg, usageTokens(1300), next, at(5))
	if len(notes2) != 0 {
		t.Errorf("second tick re-notified: %+v", notes2)
	}
}

// Dropping back resolves and DROPS the key, so the tick's diffToSave writes the
// explicit ok row. Keeping it would leave a resolved budget firing in storage.
func TestEvaluateBudgetsResolvesOnDropBack(t *testing.T) {
	cfg := tokenBudget(1000)
	firing, _ := EvaluateBudgets(cfg, usageTokens(1200), alerting.State{}, at(0))

	next, notes := EvaluateBudgets(cfg, usageTokens(10), firing, at(5))
	if len(next) != 0 {
		t.Errorf("next still holds %+v; a resolved rule must be dropped", next)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %d (%+v), want 2 resolves", len(notes), notes)
	}
	for _, n := range notes {
		if n.Kind != alerting.KindResolved || n.Status != StatusOK {
			t.Errorf("note = %+v, want a resolve reporting ok", n)
		}
	}
}

// A budget scoped to a service measures that service, not the estate. Getting
// this wrong would fire every per-service budget the moment the estate total
// crossed, which is the failure a scope exists to prevent.
func TestEvaluateBudgetsScopesToOneService(t *testing.T) {
	cfg := Config{Budgets: []Budget{{Name: "assistant", Scope: "assistant", MonthlyTokens: 1000}}}
	usage := BudgetUsage{
		TokensByService: map[string]int64{"assistant": 100, "other": 50_000},
		EstateTokens:    50_100,
	}
	_, notes := EvaluateBudgets(cfg, usage, alerting.State{}, at(0))
	if len(notes) != 0 {
		t.Errorf("a service under its budget fired on the estate total: %+v", notes)
	}
	if notes := mustFire(t, cfg, BudgetUsage{TokensByService: map[string]int64{"assistant": 1200}}); notes[0].Target != "service:assistant" {
		t.Errorf("target = %q, want service:assistant", notes[0].Target)
	}
}

func mustFire(t *testing.T, cfg Config, usage BudgetUsage) []alerting.Notification {
	t.Helper()
	_, notes := EvaluateBudgets(cfg, usage, alerting.State{}, at(0))
	if len(notes) == 0 {
		t.Fatal("expected the budget to fire")
	}
	return notes
}

// A cost budget whose scope includes unpriced calls says so in the reason. The
// number is a floor, and an operator reading "at 82%" is entitled to know the
// real figure is higher.
func TestEvaluateBudgetsSaysWhenCostIsAFloor(t *testing.T) {
	cfg := Config{
		Prices:  []Price{{Model: "gpt-4o", InputPer1MTokens: 1}},
		Budgets: []Budget{{Name: "spend", MonthlyCost: 100}},
	}
	usage := BudgetUsage{EstateCost: 90, UnpricedEstateCalls: 7}
	notes := mustFire(t, cfg, usage)
	if !strings.Contains(notes[0].Reason, "no declared price") {
		t.Errorf("reason = %q, want it to say the figure is a floor", notes[0].Reason)
	}
}

// A token budget is never a floor: tokens are counted, not priced, so the
// caveat would be false there.
func TestEvaluateBudgetsTokenBudgetIsNeverPartial(t *testing.T) {
	usage := BudgetUsage{EstateTokens: 900, UnpricedEstateCalls: 7}
	notes := mustFire(t, tokenBudget(1000), usage)
	if strings.Contains(notes[0].Reason, "no declared price") {
		t.Errorf("reason = %q; a token budget does not depend on prices", notes[0].Reason)
	}
}

// Every rule key this machine writes is namespaced, which is what lets the tick
// recognise its rows in the shared alert_state and preserve them when a usage
// recompute fails.
func TestEvaluateBudgetsNamespacesEveryKey(t *testing.T) {
	next, _ := EvaluateBudgets(tokenBudget(1000), usageTokens(1200), alerting.State{}, at(0))
	if len(next) == 0 {
		t.Fatal("nothing fired")
	}
	for k := range next {
		if len(k.Rule) < len(BudgetRulePrefix) || k.Rule[:len(BudgetRulePrefix)] != BudgetRulePrefix {
			t.Errorf("rule %q is outside the %q namespace", k.Rule, BudgetRulePrefix)
		}
	}
}
