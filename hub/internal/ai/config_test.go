package ai

import (
	"math"
	"strings"
	"testing"
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
