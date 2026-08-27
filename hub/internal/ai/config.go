// Package ai holds the pricing configuration for the AI-observability module.
// Like green and alerting it is pure logic: all SQL lives in the storage
// backend, all HTTP in the api package. See
// design/2026-08-27-ai-observability.md.
//
// There is no bundled price table and no pricing API. A bundled table would be
// stale within a month and would present a wrong number with exactly the
// confidence of a right one; an API would be the first outbound call in a
// product whose promise is that nothing leaves the cluster. An operator who
// knows what they pay writes it down; one who does not gets token counts,
// clearly labelled as token counts.
package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Price is what one model costs per million tokens. Input and output are
// separate because every provider charges them differently — usually by a
// factor of three or more, so a single blended rate would misrank the very
// workloads this exists to rank.
type Price struct {
	// Model is the id as it appears in telemetry, or a PREFIX of it: pricing
	// "gpt-4o" also prices "gpt-4o-2024-08-06", because that is the shape
	// providers actually return.
	Model             string  `json:"model"`
	InputPer1MTokens  float64 `json:"inputPer1MTokens,omitempty"`
	OutputPer1MTokens float64 `json:"outputPer1MTokens,omitempty"`
}

// Config is the whole AI configuration, loaded from the mounted ConfigMap
// (AVURUOBS_AI_CONFIG) or Default() when absent.
type Config struct {
	// Currency is a display label only — nothing converts. Empty with prices
	// set means the numbers are shown unlabelled, which is the operator's
	// choice to make.
	Currency string  `json:"currency,omitempty"`
	Prices   []Price `json:"prices,omitempty"`
}

// Default is the zero-config configuration: no prices, no currency. The
// screens then report tokens and say so.
func Default() Config { return Config{} }

// ParseConfig unmarshals and validates fail-loud — a bad price crashes the hub
// at startup, like modules.Parse and green.ParseConfig, rather than quietly
// reporting a wrong bill. Empty input yields Default().
func ParseConfig(data []byte) (Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parsing ai config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects a configuration that cannot mean what it says.
func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Prices))
	for i, p := range c.Prices {
		model := strings.TrimSpace(p.Model)
		if model == "" {
			return fmt.Errorf("ai price %d: model is required", i)
		}
		if seen[model] {
			return fmt.Errorf("ai price %d: duplicate model %q", i, model)
		}
		seen[model] = true
		if p.InputPer1MTokens < 0 || p.OutputPer1MTokens < 0 {
			return fmt.Errorf("ai price %q: rates cannot be negative", model)
		}
		// Both rates at zero is not "free", it is an entry that says nothing —
		// and it would silently shadow a prefix rule that does say something.
		if p.InputPer1MTokens == 0 && p.OutputPer1MTokens == 0 {
			return fmt.Errorf("ai price %q: set at least one of inputPer1MTokens / outputPer1MTokens", model)
		}
	}
	return nil
}

// Priced reports whether any price is configured — that is, whether money can
// be shown on the screen at all.
func (c Config) Priced() bool { return len(c.Prices) > 0 }

// Lookup resolves the price for a model: an exact id first, then the LONGEST
// declared prefix of it.
//
// byPrefix is returned so the screen can mark a row priced by a rule rather
// than by an entry someone wrote for that exact model. It is the difference
// between a number the operator stated and a number the product inferred, and
// the reader is entitled to know which one they are looking at.
func (c Config) Lookup(model string) (p Price, byPrefix bool, ok bool) {
	if model == "" {
		return Price{}, false, false
	}
	best := -1
	for i, candidate := range c.Prices {
		if candidate.Model == model {
			return candidate, false, true
		}
		if strings.HasPrefix(model, candidate.Model) &&
			(best < 0 || len(candidate.Model) > len(c.Prices[best].Model)) {
			best = i
		}
	}
	if best < 0 {
		return Price{}, false, false
	}
	return c.Prices[best], true, true
}

// Cost is what a number of tokens cost at this price. Rates are per MILLION
// tokens, which is how every provider publishes them — converting at the
// config boundary rather than here would put a factor of a million between
// what an operator typed and what they meant.
func (p Price) Cost(inputTokens, outputTokens uint64) float64 {
	const perMillion = 1_000_000.0
	return float64(inputTokens)/perMillion*p.InputPer1MTokens +
		float64(outputTokens)/perMillion*p.OutputPer1MTokens
}
