package rates

import (
	"strings"
	"testing"
)

func chartTable() Table {
	return Table{
		Currency: "EUR",
		Compute:  Compute{CPUCoreHour: 0.04, MemGiBHour: 0.005},
		Models:   []ModelPrice{{Model: "gpt-4o", InputPer1MTokens: 2.5, OutputPer1MTokens: 10}},
	}
}

// The read model service groups established: chart values stay readable and
// READ-ONLY, UI entries overlay them, and the result says which is which. A
// screen that cannot tell them apart invites an edit a `helm upgrade` silently
// reverts.
func TestMergeMarksProvenance(t *testing.T) {
	overlay := Table{Models: []ModelPrice{{Model: "claude-sonnet", InputPer1MTokens: 3}}}
	got := Merge(chartTable(), overlay)

	bySource := map[string]Provenance{}
	for _, m := range got.Models {
		bySource[m.Model] = m.Source
	}
	if bySource["gpt-4o"] != FromChart {
		t.Errorf("gpt-4o source = %q, want chart", bySource["gpt-4o"])
	}
	if bySource["claude-sonnet"] != FromOverlay {
		t.Errorf("claude-sonnet source = %q, want overlay", bySource["claude-sonnet"])
	}
	if got.CurrencySource != FromChart {
		t.Errorf("currency source = %q, want chart", got.CurrencySource)
	}
}

// A UI entry for a model the chart also declares WINS — an operator editing a
// price means it — and the row is re-marked so the screen can say so.
func TestMergeOverlayWinsOverChart(t *testing.T) {
	overlay := Table{Models: []ModelPrice{{Model: "gpt-4o", InputPer1MTokens: 99}}}
	got := Merge(chartTable(), overlay)

	if len(got.Models) != 1 {
		t.Fatalf("models = %d, want 1 (the overlay replaces, not duplicates): %+v", len(got.Models), got.Models)
	}
	if got.Models[0].InputPer1MTokens != 99 {
		t.Errorf("input rate = %v, want the overlay's 99", got.Models[0].InputPer1MTokens)
	}
	if got.Models[0].Source != FromOverlay {
		t.Errorf("source = %q, want overlay", got.Models[0].Source)
	}
}

// Compute rates move as a PAIR. Merging them field by field would let a UI that
// set only CPU silently inherit a chart memory rate, producing a blended number
// neither source ever stated.
func TestMergeComputeMovesAsAPair(t *testing.T) {
	overlay := Table{Compute: Compute{CPUCoreHour: 0.09}}
	got := Merge(chartTable(), overlay)

	if got.Compute.CPUCoreHour != 0.09 {
		t.Errorf("cpu = %v, want the overlay's 0.09", got.Compute.CPUCoreHour)
	}
	if got.Compute.MemGiBHour != 0 {
		t.Errorf("mem = %v, want 0 — the pair moved, so the chart's memory rate must not leak in",
			got.Compute.MemGiBHour)
	}
	if got.ComputeSource != FromOverlay {
		t.Errorf("compute source = %q, want overlay", got.ComputeSource)
	}
}

// An empty overlay leaves the chart exactly as declared. This is the state
// every install starts in, so it has to be the identity.
func TestMergeEmptyOverlayIsTheChart(t *testing.T) {
	got := Merge(chartTable(), Table{})
	if got.Currency != "EUR" || got.Compute.CPUCoreHour != 0.04 || len(got.Models) != 1 {
		t.Errorf("merged = %+v, want the chart unchanged", got)
	}
	if got.Models[0].Source != FromChart {
		t.Errorf("source = %q, want chart", got.Models[0].Source)
	}
}

// Prefix resolution carries over from ai.Config unchanged: exact first, then
// the LONGEST declared prefix. Changing how a price resolves while moving where
// it is stored would be two changes wearing one commit.
func TestLookupPrefersExactThenLongestPrefix(t *testing.T) {
	r := Merge(Table{Models: []ModelPrice{
		{Model: "gpt", InputPer1MTokens: 1},
		{Model: "gpt-4o", InputPer1MTokens: 2},
		{Model: "gpt-4o-2024-08-06", InputPer1MTokens: 3},
	}}, Table{})

	p, byPrefix, ok := r.Lookup("gpt-4o-2024-08-06")
	if !ok || byPrefix || p.InputPer1MTokens != 3 {
		t.Errorf("exact = %+v byPrefix=%v ok=%v, want the exact entry", p, byPrefix, ok)
	}
	p, byPrefix, ok = r.Lookup("gpt-4o-mini")
	if !ok || !byPrefix || p.InputPer1MTokens != 2 {
		t.Errorf("prefix = %+v byPrefix=%v ok=%v, want the longest prefix (gpt-4o)", p, byPrefix, ok)
	}
	if _, _, ok := r.Lookup("claude"); ok {
		t.Error("an undeclared model resolved a price")
	}
}

// Compute cost needs BOTH rates. A total built from CPU alone would look
// complete while silently omitting memory.
func TestComputePricedNeedsBothRates(t *testing.T) {
	cases := []struct {
		name string
		c    Compute
		want bool
	}{
		{"both", Compute{CPUCoreHour: 1, MemGiBHour: 1}, true},
		{"cpu only", Compute{CPUCoreHour: 1}, false},
		{"mem only", Compute{MemGiBHour: 1}, false},
		{"neither", Compute{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Merge(Table{Compute: tc.c}, Table{})
			if got := r.ComputePriced(); got != tc.want {
				t.Errorf("ComputePriced = %v, want %v", got, tc.want)
			}
		})
	}
}

// The overlay schema is CLOSED: a key this build does not know is refused
// rather than silently ignored, the same contract the collection overlay keeps.
// Silently dropping it would let an operator save a setting that never applies.
func TestParseOverlayRefusesUnknownKeys(t *testing.T) {
	_, err := ParseOverlay([]byte(`{"currency":"EUR","gpuHour":4}`))
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if !strings.Contains(err.Error(), "gpuHour") {
		t.Errorf("error = %v, want it to name the unknown key", err)
	}
}

func TestParseOverlayValidates(t *testing.T) {
	cases := []struct{ name, json, want string }{
		{"negative compute", `{"compute":{"cpuCoreHour":-1}}`, "cannot be negative"},
		{"empty model", `{"models":[{"inputPer1MTokens":1}]}`, "model is required"},
		{"duplicate model", `{"models":[{"model":"m","inputPer1MTokens":1},{"model":"m","inputPer1MTokens":2}]}`, "duplicate"},
		{"both rates zero", `{"models":[{"model":"m"}]}`, "at least one of"},
		{"negative price", `{"models":[{"model":"m","inputPer1MTokens":-1}]}`, "cannot be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseOverlay([]byte(tc.json)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Empty input is the empty table, not an error: it is what a never-saved
// overlay reads as.
func TestParseOverlayEmptyIsEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   ", "{}"} {
		got, err := ParseOverlay([]byte(in))
		if err != nil {
			t.Fatalf("ParseOverlay(%q): %v", in, err)
		}
		if !got.Empty() {
			t.Errorf("ParseOverlay(%q) = %+v, want empty", in, got)
		}
	}
}
