package api

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func spendRow(service, model string, calls uint64, in, out uint64) storage.AIServiceSpend {
	return storage.AIServiceSpend{
		Service: service, Model: model, Calls: calls, InputTokens: in, OutputTokens: out,
	}
}

// Cost is applied from the operator's declared rates HERE, not in SQL — the
// same separation the AI tables keep, and what lets the alerting tick and
// anything serving these numbers quote the same figure.
func TestAIBudgetUsagePricesFromDeclaredRates(t *testing.T) {
	fake := &storagetest.Fake{AISpendRows: []storage.AIServiceSpend{
		spendRow("assistant", "gpt-4o", 2, 1_000_000, 500_000),
		spendRow("search", "gpt-4o", 1, 500_000, 0),
	}}
	cfg := ai.Config{Prices: []ai.Price{{Model: "gpt-4o", InputPer1MTokens: 2, OutputPer1MTokens: 10}}}

	u, err := AIBudgetUsage(context.Background(), fake, cfg, "default", time.Now().UTC())
	if err != nil {
		t.Fatalf("AIBudgetUsage: %v", err)
	}

	// 1M in at 2 + 0.5M out at 10 = 2 + 5 = 7
	if got := u.CostByService["assistant"]; got != 7 {
		t.Errorf("assistant cost = %v, want 7", got)
	}
	if got := u.TokensByService["assistant"]; got != 1_500_000 {
		t.Errorf("assistant tokens = %d, want 1500000", got)
	}
	// The estate covers every service.
	if u.EstateCost != 8 {
		t.Errorf("estate cost = %v, want 8", u.EstateCost)
	}
	if u.EstateTokens != 2_000_000 {
		t.Errorf("estate tokens = %d, want 2000000", u.EstateTokens)
	}
	if u.UnpricedEstateCalls != 0 {
		t.Errorf("unpriced = %d, want 0", u.UnpricedEstateCalls)
	}
}

// An unpriced model contributes TOKENS but no money, and is counted — so a cost
// budget can say its figure is a floor. Pricing it at zero and staying quiet is
// what would let a budget come in under every threshold by being ignorant of
// half the spend.
func TestAIBudgetUsageCountsUnpricedCallsRatherThanZeroingThem(t *testing.T) {
	fake := &storagetest.Fake{AISpendRows: []storage.AIServiceSpend{
		spendRow("assistant", "gpt-4o", 2, 1_000_000, 0),
		spendRow("assistant", "claude-sonnet", 5, 4_000_000, 0),
	}}
	cfg := ai.Config{Prices: []ai.Price{{Model: "gpt-4o", InputPer1MTokens: 2}}}

	u, err := AIBudgetUsage(context.Background(), fake, cfg, "default", time.Now().UTC())
	if err != nil {
		t.Fatalf("AIBudgetUsage: %v", err)
	}
	if u.EstateCost != 2 {
		t.Errorf("estate cost = %v, want 2 (only the priced model)", u.EstateCost)
	}
	// Tokens are counted regardless of price: they are measured, not derived.
	if u.EstateTokens != 5_000_000 {
		t.Errorf("estate tokens = %d, want 5000000 (both models)", u.EstateTokens)
	}
	if u.UnpricedEstateCalls != 5 {
		t.Errorf("unpriced calls = %d, want 5", u.UnpricedEstateCalls)
	}

	// And a cost budget over this scope reports as a floor.
	if _, partial := u.Used(ai.Budget{MonthlyCost: 100}); !partial {
		t.Error("a cost budget over unpriced traffic did not report as partial")
	}
	// A token budget does not: tokens are counted, not priced.
	if _, partial := u.Used(ai.Budget{MonthlyTokens: 100}); partial {
		t.Error("a token budget reported as partial")
	}
}

// Prefix pricing carries through, so "gpt-4o" prices the dated build a provider
// actually answers with.
func TestAIBudgetUsagePricesByPrefix(t *testing.T) {
	fake := &storagetest.Fake{AISpendRows: []storage.AIServiceSpend{
		spendRow("assistant", "gpt-4o-2024-08-06", 1, 1_000_000, 0),
	}}
	cfg := ai.Config{Prices: []ai.Price{{Model: "gpt-4o", InputPer1MTokens: 3}}}

	u, err := AIBudgetUsage(context.Background(), fake, cfg, "default", time.Now().UTC())
	if err != nil {
		t.Fatalf("AIBudgetUsage: %v", err)
	}
	if u.EstateCost != 3 {
		t.Errorf("estate cost = %v, want 3 (priced by prefix)", u.EstateCost)
	}
	if u.UnpricedEstateCalls != 0 {
		t.Errorf("unpriced = %d, want 0", u.UnpricedEstateCalls)
	}
}

// The month-to-date window is what a monthly budget means. A rolling window
// would make a budget that never resets.
func TestAIBudgetUsageReadsMonthToDate(t *testing.T) {
	fake := &storagetest.Fake{}
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)

	if _, err := AIBudgetUsage(context.Background(), fake, ai.Default(), "default", now); err != nil {
		t.Fatalf("AIBudgetUsage: %v", err)
	}
	start := fake.LastAIQuery.Range.Start
	if start.Day() != 1 || start.Month() != time.September || start.Year() != 2026 {
		t.Errorf("range start = %v, want 2026-09-01", start)
	}
	if !fake.LastAIQuery.ExcludeAux {
		t.Error("the product's own telemetry was not excluded from a spend total")
	}
}
