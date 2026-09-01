package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/alerting"
	"github.com/avuru/avuru-obs/hub/internal/health"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// budgetAIConfig is one estate-wide token budget of 1000, warning at 80%.
func budgetAIConfig() ai.Config {
	return ai.Config{
		BudgetCheckIntervalSec: 300,
		Budgets: []ai.Budget{{
			Name: "estate", MonthlyTokens: 1000, WarnRatio: 0.8, Channel: "ops",
		}},
	}
}

func aiBudgetsFor(t *testing.T, usage aiUsageFn) *aiBudgets {
	t.Helper()
	return newAIBudgets(
		modules.Set{modules.AI: true, modules.Alerting: true},
		budgetAIConfig,
		newAIUsageCache(usage),
	)
}

// The hazard BudgetRulePrefix exists to prevent. diffToSave writes an explicit
// ok row for every prev key absent from next; ai:* keys live in the shared
// alert_state but are NOT produced by alerting.Evaluate. Without the merge, a
// firing spend budget is clobbered to ok on every tick — an alert that silently
// un-fires while the spend is still over.
func TestEvaluateOnceMergesAIBudgetsWithoutClobber(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	fake := &storagetest.Fake{
		AISpendRows: []storage.AIServiceSpend{
			{Service: "assistant", Model: "gpt-4o", Calls: 3, InputTokens: 1000, OutputTokens: 200},
		},
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "ai:estate:warn", Target: "estate", Status: "firing", Since: old},
			{Tenant: "default", RuleName: "ai:estate:over", Target: "estate", Status: "firing", Since: old},
		},
	}

	ab := aiBudgetsFor(t, defaultAIBudgetUsage)
	notifier := &captureNotifier{}

	if err := evaluateOnce(context.Background(), func() storage.Store { return fake },
		health.Default(), alerting.Default(), notifier, nil, nil, ab, now); err != nil {
		t.Fatalf("evaluateOnce: %v", err)
	}

	if len(fake.SavedAlertStates) != 1 {
		t.Fatalf("want one saved batch, got %d", len(fake.SavedAlertStates))
	}
	saved := savedByRule(fake.SavedAlertStates[0])
	// 1200 tokens against a budget of 1000: both rules stay firing.
	if saved["ai:estate:warn"] != "firing" {
		t.Errorf("ai:estate:warn saved as %q, want firing (diffToSave clobber not prevented)", saved["ai:estate:warn"])
	}
	if saved["ai:estate:over"] != "firing" {
		t.Errorf("ai:estate:over saved as %q, want firing", saved["ai:estate:over"])
	}
}

// A transient usage failure must PRESERVE firing budgets rather than resolve
// them. A ClickHouse blip is not evidence that spend came back under budget,
// and treating it as such would silently close a live alert.
func TestEvaluateOnceKeepsFiringAIBudgetsWhenUsageFails(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	fake := &storagetest.Fake{
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "ai:estate:over", Target: "estate", Status: "firing", Since: old},
		},
	}

	ab := aiBudgetsFor(t, func(context.Context, storage.Store, ai.Config, string, time.Time) (ai.BudgetUsage, error) {
		return ai.BudgetUsage{}, errors.New("clickhouse unreachable")
	})

	if err := evaluateOnce(context.Background(), func() storage.Store { return fake },
		health.Default(), alerting.Default(), &captureNotifier{}, nil, nil, ab, now); err != nil {
		t.Fatalf("evaluateOnce: %v", err)
	}

	saved := savedByRule(fake.SavedAlertStates[0])
	if saved["ai:estate:over"] != "firing" {
		t.Errorf("ai:estate:over saved as %q, want firing — a failed recompute must not resolve a budget",
			saved["ai:estate:over"])
	}
}

// The month-wide scan is throttled. The alerting tick runs every 30s by
// default and a monthly total does not move that fast.
func TestAIUsageCacheThrottlesRecompute(t *testing.T) {
	var calls int
	counting := func(context.Context, storage.Store, ai.Config, string, time.Time) (ai.BudgetUsage, error) {
		calls++
		return ai.BudgetUsage{EstateTokens: 10}, nil
	}
	cache := newAIUsageCache(counting)
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		if _, err := cache.get(context.Background(), nil, budgetAIConfig(), "default",
			now.Add(time.Duration(i)*time.Second), 5*time.Minute); err != nil {
			t.Fatalf("get: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("computed %d times within the interval, want 1", calls)
	}

	if _, err := cache.get(context.Background(), nil, budgetAIConfig(), "default",
		now.Add(6*time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("get: %v", err)
	}
	if calls != 2 {
		t.Errorf("computed %d times after the interval elapsed, want 2", calls)
	}
}

// Budgets ride the alerting seam. With either module off there is no tick to
// carry them, and the hook is nil rather than half-wired.
func TestNewAIBudgetsNilWithoutBothModules(t *testing.T) {
	cache := newAIUsageCache(defaultAIBudgetUsage)
	cases := []struct {
		name string
		set  modules.Set
		want bool
	}{
		{"both on", modules.Set{modules.AI: true, modules.Alerting: true}, true},
		{"alerting off", modules.Set{modules.AI: true}, false},
		{"ai off", modules.Set{modules.Alerting: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newAIBudgets(tc.set, budgetAIConfig, cache) != nil
			if got != tc.want {
				t.Errorf("newAIBudgets non-nil = %v, want %v", got, tc.want)
			}
		})
	}
}
