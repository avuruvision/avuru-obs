// Package rates is the ONE rate table.
//
// Before this, an operator declaring what their estate costs did it twice, in
// two formats, one of which needed a pod restart: AI prices arrived as a
// mounted ConfigMap, hot-reloaded and validated fail-loud, while cost rates were
// three environment variables read once at startup with a warning on a bad
// value. Two independent Currency fields could disagree and nothing noticed.
//
// Both now resolve through here. Like health.Resolver, this package is pure
// merge logic: no SQL, no HTTP. The shape of the document is owned here; the
// store keeps it as an opaque blob.
package rates

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelPrice is what one model costs per million tokens — the shape ai.Price
// already had, carried across unchanged so an operator's existing values keep
// meaning what they meant.
type ModelPrice struct {
	// Model is the id as it appears in telemetry, or a PREFIX of it: pricing
	// "gpt-4o" also prices "gpt-4o-2024-08-06".
	Model             string  `json:"model"`
	InputPer1MTokens  float64 `json:"inputPer1MTokens,omitempty"`
	OutputPer1MTokens float64 `json:"outputPer1MTokens,omitempty"`
}

// Compute is what reserved capacity costs per hour.
type Compute struct {
	CPUCoreHour float64 `json:"cpuCoreHour,omitempty"`
	MemGiBHour  float64 `json:"memGiBHour,omitempty"`
}

// Table is the whole rate table: one currency, the compute rates, and the model
// prices.
//
// ONE Currency, which is the point. Two independent fields could render EUR on
// the AI screen and USD on the Cost screen with nothing noticing, and an
// operator reading a total across both had no way to know.
type Table struct {
	Currency string       `json:"currency,omitempty"`
	Compute  Compute      `json:"compute,omitempty"`
	Models   []ModelPrice `json:"models,omitempty"`
}

// Empty reports whether this table declares nothing at all.
func (t Table) Empty() bool {
	return t.Currency == "" && t.Compute == (Compute{}) && len(t.Models) == 0
}

// Validate rejects a table that cannot mean what it says. Shared by the
// chart-declared path (fail-loud at startup) and the UI write path (rejected on
// the PUT), so a value the API accepts is one the config would have accepted.
func (t Table) Validate() error {
	if t.Compute.CPUCoreHour < 0 || t.Compute.MemGiBHour < 0 {
		return fmt.Errorf("compute rates cannot be negative")
	}
	seen := make(map[string]bool, len(t.Models))
	for i, m := range t.Models {
		model := strings.TrimSpace(m.Model)
		if model == "" {
			return fmt.Errorf("model price %d: model is required", i)
		}
		if seen[model] {
			return fmt.Errorf("model price %d: duplicate model %q", i, model)
		}
		seen[model] = true
		if m.InputPer1MTokens < 0 || m.OutputPer1MTokens < 0 {
			return fmt.Errorf("model price %q: rates cannot be negative", model)
		}
		// Both rates at zero is not "free", it is an entry that says nothing —
		// and it would silently shadow a prefix rule that does say something.
		if m.InputPer1MTokens == 0 && m.OutputPer1MTokens == 0 {
			return fmt.Errorf("model price %q: set at least one of inputPer1MTokens / outputPer1MTokens", model)
		}
	}
	return nil
}

// ParseOverlay decodes a stored overlay document with a CLOSED schema, so a key
// this build does not know is refused rather than silently ignored — the same
// contract the collection overlay keeps. Empty input is the empty table.
func ParseOverlay(data []byte) (Table, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Table{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var t Table
	if err := dec.Decode(&t); err != nil {
		return Table{}, fmt.Errorf("parsing rates overlay: %w", err)
	}
	if err := t.Validate(); err != nil {
		return Table{}, err
	}
	return t, nil
}

// Provenance says where a resolved value came from, so the screen can mark
// chart-declared entries read-only rather than inviting an edit that a
// `helm upgrade` will silently revert.
type Provenance string

const (
	FromChart   Provenance = "chart"
	FromOverlay Provenance = "overlay"
)

// PricedModel is a resolved model price with its origin attached.
type PricedModel struct {
	ModelPrice
	Source Provenance `json:"source"`
}

// Resolved is the merged table the product actually reads.
type Resolved struct {
	Currency       string        `json:"currency,omitempty"`
	CurrencySource Provenance    `json:"currencySource,omitempty"`
	Compute        Compute       `json:"compute"`
	ComputeSource  Provenance    `json:"computeSource,omitempty"`
	Models         []PricedModel `json:"models"`
}

// Merge overlays UI-authored entries onto chart-declared ones.
//
// The read model is service groups': chart values stay READABLE and read-only,
// UI entries overlay them, and the result says which is which. A UI entry for a
// model the chart also declares wins — an operator editing a price in the UI
// means it — but the row is still marked so the screen can say the chart
// disagrees.
func Merge(chart, overlay Table) Resolved {
	out := Resolved{
		Currency:       chart.Currency,
		CurrencySource: FromChart,
		Compute:        chart.Compute,
		ComputeSource:  FromChart,
		Models:         []PricedModel{},
	}
	if overlay.Currency != "" {
		out.Currency, out.CurrencySource = overlay.Currency, FromOverlay
	}
	// Compute rates move as a PAIR. Merging them field by field would let a UI
	// that sets only CPU silently inherit a chart memory rate, producing a
	// blended number neither source ever stated.
	if overlay.Compute != (Compute{}) {
		out.Compute, out.ComputeSource = overlay.Compute, FromOverlay
	}

	byModel := map[string]int{}
	for _, m := range chart.Models {
		byModel[m.Model] = len(out.Models)
		out.Models = append(out.Models, PricedModel{ModelPrice: m, Source: FromChart})
	}
	for _, m := range overlay.Models {
		if i, ok := byModel[m.Model]; ok {
			out.Models[i] = PricedModel{ModelPrice: m, Source: FromOverlay}
			continue
		}
		byModel[m.Model] = len(out.Models)
		out.Models = append(out.Models, PricedModel{ModelPrice: m, Source: FromOverlay})
	}
	return out
}

// Lookup resolves the price for a model: an exact id first, then the LONGEST
// declared prefix of it. Carried over from ai.Config.Lookup unchanged, because
// changing how a price resolves while moving where it is stored would be two
// changes wearing one commit.
func (r Resolved) Lookup(model string) (p ModelPrice, byPrefix bool, ok bool) {
	if model == "" {
		return ModelPrice{}, false, false
	}
	best := -1
	for i, c := range r.Models {
		if c.Model == model {
			return c.ModelPrice, false, true
		}
		if strings.HasPrefix(model, c.Model) &&
			(best < 0 || len(c.Model) > len(r.Models[best].Model)) {
			best = i
		}
	}
	if best < 0 {
		return ModelPrice{}, false, false
	}
	return r.Models[best].ModelPrice, true, true
}

// Priced reports whether any model price is resolved at all.
func (r Resolved) Priced() bool { return len(r.Models) > 0 }

// ComputePriced reports whether compute cost can be shown. BOTH rates are
// required: a cost built from CPU alone would look like a total while silently
// omitting memory.
func (r Resolved) ComputePriced() bool {
	return r.Compute.CPUCoreHour > 0 && r.Compute.MemGiBHour > 0
}

// Cost is what a number of tokens cost at this price. Rates are per MILLION
// tokens, which is how every provider publishes them.
func (p ModelPrice) Cost(inputTokens, outputTokens uint64) float64 {
	const perMillion = 1_000_000.0
	return float64(inputTokens)/perMillion*p.InputPer1MTokens +
		float64(outputTokens)/perMillion*p.OutputPer1MTokens
}
