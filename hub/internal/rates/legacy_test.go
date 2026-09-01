package rates

import (
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/ai"
)

// Both legacy configurations keep working exactly as they did. GitOps installs
// declare everything in values, and breaking them to move a table into a
// database would trade one problem for a worse one.
func TestFromLegacyCarriesBothSources(t *testing.T) {
	aiCfg := ai.Config{
		Currency: "EUR",
		Prices:   []ai.Price{{Model: "gpt-4o", InputPer1MTokens: 2.5, OutputPer1MTokens: 10}},
	}
	got, warnings := FromLegacy(aiCfg, 0.04, 0.005, "EUR")

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none when the currencies agree", warnings)
	}
	if got.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", got.Currency)
	}
	if got.Compute.CPUCoreHour != 0.04 || got.Compute.MemGiBHour != 0.005 {
		t.Errorf("compute = %+v, want the env rates", got.Compute)
	}
	if len(got.Models) != 1 || got.Models[0].OutputPer1MTokens != 10 {
		t.Errorf("models = %+v, want the ConfigMap price carried over", got.Models)
	}
}

// The two Currency fields were independent and could disagree. An install that
// set them differently now gets a warning naming BOTH, rather than a silent
// pick that leaves one screen in EUR and another in USD.
func TestFromLegacyWarnsWhenCurrenciesDisagree(t *testing.T) {
	got, warnings := FromLegacy(ai.Config{Currency: "EUR"}, 0.04, 0.005, "USD")

	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	w := warnings[0]
	if !strings.Contains(w, "EUR") || !strings.Contains(w, "USD") {
		t.Errorf("warning = %q, want it to name BOTH currencies", w)
	}
	if got.Currency != "EUR" {
		t.Errorf("currency = %q, want the AI one to win (and be named)", got.Currency)
	}
}

// One side set is not a disagreement.
func TestFromLegacyNoWarningWhenOnlyOneIsSet(t *testing.T) {
	cases := []struct {
		name         string
		aiCur, cost  string
		wantCurrency string
	}{
		{"ai only", "EUR", "", "EUR"},
		{"cost only", "", "USD", "USD"},
		{"neither", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := FromLegacy(ai.Config{Currency: tc.aiCur}, 0, 0, tc.cost)
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
			if got.Currency != tc.wantCurrency {
				t.Errorf("currency = %q, want %q", got.Currency, tc.wantCurrency)
			}
		})
	}
}

// An install that declares nothing produces an empty table, which the screens
// report as "not priced" rather than as free.
func TestFromLegacyEmptyIsEmpty(t *testing.T) {
	got, warnings := FromLegacy(ai.Default(), 0, 0, "")
	if !got.Empty() {
		t.Errorf("table = %+v, want empty", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}
