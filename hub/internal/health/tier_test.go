package health

import "testing"

// TestMoreCritical: the group conflict rule takes the most critical tier, so a
// group holding one T0 service is a T0 group regardless of member order.
func TestMoreCritical(t *testing.T) {
	cases := []struct{ a, b, want Tier }{
		{TierT2, TierT0, TierT0},
		{TierT0, TierT2, TierT0},
		{TierT1, TierT3, TierT1},
		{TierT2, TierT2, TierT2},
		{TierT3, TierT0, TierT0},
	}
	for _, c := range cases {
		if got := moreCritical(c.a, c.b); got != c.want {
			t.Errorf("moreCritical(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// TestParseTierSoft: declared tiers are untrusted input — a valid value parses,
// anything else reports ok=false so the caller can fall back and warn.
func TestParseTierSoft(t *testing.T) {
	valid := map[string]Tier{"T0": TierT0, "T1": TierT1, "T2": TierT2, "T3": TierT3}
	for in, want := range valid {
		got, ok := parseTierSoft(in)
		if !ok || got != want {
			t.Errorf("parseTierSoft(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "T9", "t0", "critical", "0"} {
		if got, ok := parseTierSoft(in); ok {
			t.Errorf("parseTierSoft(%q) = (%q, true), want ok=false", in, got)
		}
	}
}
