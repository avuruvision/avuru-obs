package rates

import (
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/ai"
)

// FromLegacy builds the chart-declared table from the two configurations that
// existed before this package: the AI price ConfigMap and the cost environment
// variables.
//
// Both keep working exactly as they did — an install that declares either one
// sees no change — which is the whole reason this is a merge rather than a
// replacement. GitOps installs declare everything in values, and breaking them
// to move a table into a database would trade one problem for a worse one.
//
// It returns any warnings the operator should see at startup: the currencies
// were two independent fields and could disagree, and an install that set them
// differently deserves to be told which one won rather than to discover it on a
// screen.
func FromLegacy(aiCfg ai.Config, cpuCoreHour, memGiBHour float64, costCurrency string) (Table, []string) {
	t := Table{
		Compute: Compute{CPUCoreHour: cpuCoreHour, MemGiBHour: memGiBHour},
	}
	for _, p := range aiCfg.Prices {
		t.Models = append(t.Models, ModelPrice{
			Model:             p.Model,
			InputPer1MTokens:  p.InputPer1MTokens,
			OutputPer1MTokens: p.OutputPer1MTokens,
		})
	}

	var warnings []string
	switch {
	case aiCfg.Currency != "" && costCurrency != "" && aiCfg.Currency != costCurrency:
		// Name BOTH. A silent pick is how an estate ends up with one screen in
		// EUR and another in USD and a total that means nothing.
		t.Currency = aiCfg.Currency
		warnings = append(warnings, fmt.Sprintf(
			"ai.currency (%q) and cost currency (%q) disagree; using %q — set them to the same value, "+
				"or declare one currency in the rate table", aiCfg.Currency, costCurrency, aiCfg.Currency))
	case aiCfg.Currency != "":
		t.Currency = aiCfg.Currency
	default:
		t.Currency = costCurrency
	}
	return t, warnings
}
