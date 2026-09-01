package ai

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseConfigEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\n"} {
		cfg, err := ParseConfig([]byte(in))
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", in, err)
		}
		if cfg.Priced() {
			t.Errorf("ParseConfig(%q) should be unpriced, got %+v", in, cfg)
		}
	}
}

// Fail-loud, like modules.Parse and green.ParseConfig: a price that cannot
// mean what it says must stop the hub, not quietly produce a wrong bill.
func TestParseConfigRejectsUnusableEntries(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no model", `{"prices":[{"inputPer1MTokens":1}]}`, "model is required"},
		{"duplicate model", `{"prices":[{"model":"m","inputPer1MTokens":1},{"model":"m","outputPer1MTokens":2}]}`, "duplicate"},
		{"negative rate", `{"prices":[{"model":"m","inputPer1MTokens":-1}]}`, "negative"},
		{"says nothing at all", `{"prices":[{"model":"m"}]}`, "at least one"},
		{"unknown field", `{"prices":[{"model":"m","inputPerToken":1}]}`, "unknown field"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.in))
			if err == nil {
				t.Fatalf("ParseConfig(%s) should fail", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q: %v", tc.want, err)
			}
		})
	}
}

// An exact entry always beats a prefix, and among prefixes the longest wins —
// otherwise declaring a cheap family rate would silently underprice a specific
// model somebody took the trouble to name.
func TestLookupPrefersExactThenLongestPrefix(t *testing.T) {
	cfg := Config{Prices: []Price{
		{Model: "gpt-4", InputPer1MTokens: 30},
		{Model: "gpt-4o", InputPer1MTokens: 2.5},
		{Model: "gpt-4o-mini", InputPer1MTokens: 0.15},
	}}
	tests := []struct {
		model        string
		wantIn       float64
		wantByPrefix bool
		wantOK       bool
	}{
		{"gpt-4o", 2.5, false, true},
		{"gpt-4o-2024-08-06", 2.5, true, true},
		{"gpt-4o-mini", 0.15, false, true},
		{"gpt-4o-mini-2024-07-18", 0.15, true, true},
		{"gpt-4-turbo", 30, true, true},
		{"claude-sonnet", 0, false, false},
		{"", 0, false, false},
	}
	for _, tc := range tests {
		p, byPrefix, ok := cfg.Lookup(tc.model)
		if ok != tc.wantOK || byPrefix != tc.wantByPrefix || p.InputPer1MTokens != tc.wantIn {
			t.Errorf("Lookup(%q) = %v/%v/%v, want %v/%v/%v",
				tc.model, p.InputPer1MTokens, byPrefix, ok, tc.wantIn, tc.wantByPrefix, tc.wantOK)
		}
	}
}

// Input and output are priced apart because every provider charges them
// differently; blending them would misrank exactly the workloads the screen
// exists to rank.
func TestCostPricesInputAndOutputSeparately(t *testing.T) {
	p := Price{Model: "m", InputPer1MTokens: 2.5, OutputPer1MTokens: 10}
	got := p.Cost(1_000_000, 500_000)
	want := 2.5 + 5.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost = %v, want %v", got, want)
	}
	if c := p.Cost(0, 0); c != 0 {
		t.Errorf("Cost(0,0) = %v, want 0", c)
	}
}

// A price declared only on one side must not price the other side as free by
// accident — it prices it at zero because that is what was declared, and the
// screen's unpriced-model reporting is what catches a half-filled table.
func TestCostWithOneSidedRate(t *testing.T) {
	p := Price{Model: "m", OutputPer1MTokens: 10}
	if got := p.Cost(1_000_000, 1_000_000); math.Abs(got-10) > 1e-9 {
		t.Errorf("Cost = %v, want 10", got)
	}
}

// A cost budget over an estate with no prices measures against a floor of zero:
// it would come in under every threshold by being ignorant of the whole bill,
// and would never fire. Refused where the rest of the config is refused —
// at parse time, on the config, not discovered from an alert that never arrives.
func TestParseConfigRefusesACostBudgetWithNoPrices(t *testing.T) {
	_, err := ParseConfig([]byte(`{"budgets":[{"name":"spend","monthlyCost":100}]}`))
	if err == nil {
		t.Fatal("a cost budget with no prices was accepted")
	}
	if !strings.Contains(err.Error(), "needs prices declared") {
		t.Errorf("error = %v, want it to name the missing prices", err)
	}
}

// The same budget denominated in TOKENS is fine without prices — tokens are
// counted, not priced, which is the whole reason both units exist.
func TestParseConfigAcceptsATokenBudgetWithNoPrices(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"budgets":[{"name":"spend","monthlyTokens":1000000}]}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].MonthlyTokens != 1_000_000 {
		t.Errorf("budgets = %+v", cfg.Budgets)
	}
}

func TestParseConfigRejectsUnusableBudgets(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"no name", `{"budgets":[{"monthlyTokens":10}]}`, "name is required"},
		{"no unit", `{"budgets":[{"name":"a"}]}`, "set one of"},
		{"both units", `{"budgets":[{"name":"a","monthlyTokens":10,"monthlyCost":5}],"prices":[{"model":"m","inputPer1MTokens":1}]}`, "not both"},
		{"negative", `{"budgets":[{"name":"a","monthlyTokens":-1}]}`, "cannot be negative"},
		{"duplicate", `{"budgets":[{"name":"a","monthlyTokens":10},{"name":"a","monthlyTokens":20}]}`, "duplicate"},
		{"warn ratio at 1", `{"budgets":[{"name":"a","monthlyTokens":10,"warnRatio":1}]}`, "between 0 and 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.json))
			if err == nil {
				t.Fatalf("accepted %s", tc.json)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The budget check interval falls back rather than being zero, which would
// recompute a month-wide scan on every alerting tick.
func TestBudgetCheckIntervalDefaults(t *testing.T) {
	if got := Default().BudgetCheckInterval(); got != 300*time.Second {
		t.Errorf("interval = %v, want 5m", got)
	}
	cfg := Config{BudgetCheckIntervalSec: 30}
	if got := cfg.BudgetCheckInterval(); got != 30*time.Second {
		t.Errorf("interval = %v, want 30s", got)
	}
}
