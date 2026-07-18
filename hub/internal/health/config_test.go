package health

import "testing"

func TestParseConfigEmptyIsDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		got, err := ParseConfig([]byte(in))
		if err != nil {
			t.Fatalf("ParseConfig(%q) error: %v", in, err)
		}
		if got.DefaultTier != TierT2 {
			t.Errorf("ParseConfig(%q) DefaultTier = %q, want T2", in, got.DefaultTier)
		}
		if len(got.Groups) != 0 {
			t.Errorf("ParseConfig(%q) expected no groups, got %d", in, len(got.Groups))
		}
	}
}

func TestParseConfigValidatesFailLoud(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"bad defaultTier", `{"defaultTier":"T9"}`},
		{"empty group name", `{"defaultTier":"T0","groups":[{"name":"","tier":"T0","selector":{"namespaces":["a"]}}]}`},
		{"duplicate group name", `{"defaultTier":"T0","groups":[
			{"name":"x","tier":"T0","selector":{"namespaces":["a"]}},
			{"name":"x","tier":"T1","selector":{"namespaces":["b"]}}]}`},
		{"invalid group tier", `{"defaultTier":"T0","groups":[{"name":"x","tier":"gold","selector":{"namespaces":["a"]}}]}`},
		{"empty selector", `{"defaultTier":"T0","groups":[{"name":"x","tier":"T0","selector":{}}]}`},
		{"invalid tier threshold key", `{"defaultTier":"T0","thresholds":{"tiers":{"T5":{"errorRateWarn":0.1}}}}`},
		{"unknown field", `{"defaultTier":"T0","bogus":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tt.json)); err == nil {
				t.Errorf("ParseConfig(%s) expected error, got nil", tt.json)
			}
		})
	}
}

func TestParseConfigValid(t *testing.T) {
	const cfg = `{
	  "defaultTier": "T2",
	  "groups": [
	    {"name":"identity","tier":"T0","selector":{"namespaces":["auth"],"services":["token-svc"]}},
	    {"name":"payments","tier":"T0","selector":{"namespaces":["payments"]}}
	  ],
	  "thresholds": {
	    "defaults": {"errorRateWarn":0.02},
	    "tiers": {"T0": {"errorRateCrit":0.01}},
	    "services": {"payments": {"latencyP95ObjectiveMs":800}}
	  },
	  "criticalEdges": [{"from":"web","to":"legacy"}]
	}`
	c, err := ParseConfig([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if len(c.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(c.Groups))
	}
	// Global default override applied; built-in fills the rest.
	got := c.ResolveThresholds("web", TierT1)
	if got.ErrorRateWarn != 0.02 {
		t.Errorf("global warn override: got %v want 0.02", got.ErrorRateWarn)
	}
	if got.ErrorRateCrit != 0.05 {
		t.Errorf("built-in crit should survive: got %v want 0.05", got.ErrorRateCrit)
	}
	// Precedence: service > tier > default.
	pay := c.ResolveThresholds("payments", TierT0)
	if pay.ErrorRateCrit != 0.01 {
		t.Errorf("tier T0 crit override: got %v want 0.01", pay.ErrorRateCrit)
	}
	if pay.LatencyP95ObjectiveMs != 800 {
		t.Errorf("service latency override: got %v want 800", pay.LatencyP95ObjectiveMs)
	}
	if !c.criticalOverrides()[[2]string{"web", "legacy"}] {
		t.Errorf("critical override not indexed")
	}
}
