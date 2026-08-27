package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// aiMux builds a router with prices declared, which newMux deliberately leaves
// unset (an install with no rates is the default this product ships).
func aiMux(fake *storagetest.Fake, cfg ai.Config) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		AIConfig: func() ai.Config { return cfg },
	})
	return mux
}

func decodeInto(t *testing.T, mux *http.ServeMux, path string, out any) {
	t.Helper()
	rec := get(t, mux, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func modelRow(name string, calls, in, out uint64) storage.AIModelUsage {
	return storage.AIModelUsage{
		Model: name, Provider: "openai", Calls: calls,
		InputTokens: in, OutputTokens: out, P95: 250 * time.Millisecond,
	}
}

// The filter vocabulary must reach storage intact: an AI screen filtered to a
// service and the trace list filtered the same way have to describe the same
// traffic, or the two numbers on one page disagree.
func TestAIQueryCarriesTheTraceFilters(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := aiMux(fake, ai.Default())
	get(t, mux, "/api/v1/ai/models?service=checkout&model=gpt-4o&tags=env%3Dprod&limit=7")

	q := fake.LastAIQuery
	if q.Service != "checkout" || q.Model != "gpt-4o" || q.Limit != 7 {
		t.Errorf("filters lost: %+v", q)
	}
	if q.Tags["env"] != "prod" {
		t.Errorf("tag filter lost: %+v", q.Tags)
	}
	// Aux exclusion is on unless asked for, exactly as the trace search does.
	if !q.ExcludeAux {
		t.Error("ExcludeAux should default to true")
	}
	get(t, mux, "/api/v1/ai/models?includeAux=true")
	if fake.LastAIQuery.ExcludeAux {
		t.Error("includeAux=true should clear ExcludeAux")
	}
}

// With no rates declared there is no currency, no cost, and nothing that could
// be mistaken for money. An install that has not priced its models gets token
// counts and is told that is what it is looking at.
func TestAIModelsUnpricedShowsNoMoney(t *testing.T) {
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models:     []storage.AIModelUsage{modelRow("gpt-4o", 10, 1000, 500)},
		Total:      storage.AIModelUsage{Calls: 10, InputTokens: 1000, OutputTokens: 500},
		ModelCount: 1,
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, ai.Default()), "/api/v1/ai/models", &resp)

	if resp.Priced || resp.Currency != "" {
		t.Errorf("unpriced install should report no currency: %+v", resp)
	}
	if resp.Models[0].Cost != nil || resp.Total.Cost != nil {
		t.Error("no rates declared must mean no cost at all, not a zero")
	}
	// The totals row describes the window, not a model: SQL picks a provider
	// from whichever call declared one, which means nothing here.
	if resp.Total.Model != "" || resp.Total.Provider != "" {
		t.Errorf("totals should carry no identity: %+v", resp.Total)
	}
	// And nothing is "unpriced" either — there is no price table to be missing
	// from, so naming models would be noise.
	if len(resp.UnpricedModels) != 0 {
		t.Errorf("unpriced list should be empty with no rates: %v", resp.UnpricedModels)
	}
}

// A model with no rate is listed as unpriced rather than costed at zero, and
// the total says so by being a floor.
func TestAIModelsNamesUnpricedModels(t *testing.T) {
	cfg := ai.Config{Currency: "EUR", Prices: []ai.Price{
		{Model: "gpt-4o", InputPer1MTokens: 2.5, OutputPer1MTokens: 10},
	}}
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{
			modelRow("gpt-4o", 10, 1_000_000, 1_000_000),
			modelRow("llama-3-70b", 4, 2_000_000, 0),
		},
		Total:      storage.AIModelUsage{Calls: 14, InputTokens: 3_000_000, OutputTokens: 1_000_000},
		ModelCount: 2,
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, cfg), "/api/v1/ai/models", &resp)

	if !resp.Priced || resp.Currency != "EUR" {
		t.Errorf("priced install should report its currency: %+v", resp)
	}
	if resp.Models[0].Cost == nil || *resp.Models[0].Cost != 12.5 {
		t.Errorf("gpt-4o cost = %v, want 12.5", resp.Models[0].Cost)
	}
	if resp.Models[1].Cost != nil {
		t.Error("a model with no rate must have no cost, not a zero")
	}
	if len(resp.UnpricedModels) != 1 || resp.UnpricedModels[0] != "llama-3-70b" {
		t.Errorf("unpriced models = %v, want [llama-3-70b]", resp.UnpricedModels)
	}
	// The total is the sum of what could be priced — a floor, not a claim
	// about the whole window.
	if resp.Total.Cost == nil || *resp.Total.Cost != 12.5 {
		t.Errorf("total cost = %v, want 12.5", resp.Total.Cost)
	}
}

// A cost derived from a prefix rule is marked as such: it is a number the
// product inferred, not one the operator wrote down for that model.
func TestAIModelsMarksPrefixPricing(t *testing.T) {
	cfg := ai.Config{Prices: []ai.Price{{Model: "gpt-4o", InputPer1MTokens: 2.5}}}
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{modelRow("gpt-4o-2024-08-06", 1, 1_000_000, 0)},
		Total:  storage.AIModelUsage{Calls: 1, InputTokens: 1_000_000},
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, cfg), "/api/v1/ai/models", &resp)
	if !resp.Models[0].PricedByPrefix {
		t.Error("a prefix-derived price must be marked")
	}
}

// The tail is arithmetic on the totals — which cover the whole window — so a
// truncated table cannot redraw its top N as the entire estate. Latency is not
// subtractable and must come back zero.
func TestAIModelsDerivesTheTailFromTotals(t *testing.T) {
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{modelRow("gpt-4o", 10, 1000, 500)},
		Total: storage.AIModelUsage{
			Calls: 25, Errors: 3, Truncated: 2, InputTokens: 4000, OutputTokens: 900,
			P95: 900 * time.Millisecond,
		},
		ModelCount: 6,
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, ai.Default()), "/api/v1/ai/models", &resp)

	if resp.Other == nil {
		t.Fatal("a window with more models than rows must report a tail")
	}
	if resp.Other.Calls != 15 || resp.Other.InputTokens != 3000 || resp.Other.OutputTokens != 400 {
		t.Errorf("tail = %+v, want 15 calls / 3000 in / 400 out", resp.Other)
	}
	if resp.Other.P95Ms != 0 {
		t.Errorf("tail quantiles cannot be recovered by subtraction: %v", resp.Other.P95Ms)
	}
	if resp.ModelCount != 6 {
		t.Errorf("modelCount = %d, want the distinct count 6", resp.ModelCount)
	}
}

// A complete table has no tail — an "Other: 0" row is noise that reads as a
// finding.
func TestAIModelsNoTailWhenComplete(t *testing.T) {
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models:     []storage.AIModelUsage{modelRow("gpt-4o", 10, 1000, 500)},
		Total:      storage.AIModelUsage{Calls: 10, InputTokens: 1000, OutputTokens: 500},
		ModelCount: 1,
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, ai.Default()), "/api/v1/ai/models", &resp)
	if resp.Other != nil {
		t.Errorf("complete table should have no tail: %+v", resp.Other)
	}
}

// The three counters that keep the screen honest survive the DTO: truncation
// is not an error, a call that reported no usage is not a call that used
// nothing, and content arriving is reported rather than rendered.
func TestAIModelsCarriesTheHonestyCounters(t *testing.T) {
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{{
			Model: "gpt-4o", Calls: 100, Errors: 2, Refused: 1, Truncated: 9,
			CallsWithoutUsage: 4, CallsFromRequestModel: 7, CallsWithContent: 40,
		}},
		Total:      storage.AIModelUsage{Calls: 100, Truncated: 9, CallsWithContent: 40},
		ModelCount: 1,
	}}
	var resp aiModelsResponse
	decodeInto(t, aiMux(fake, ai.Default()), "/api/v1/ai/models", &resp)

	m := resp.Models[0]
	if m.Truncated != 9 {
		t.Errorf("truncated = %d, want 9", m.Truncated)
	}
	if m.Errors+m.Refused != 3 {
		t.Errorf("truncation must not be folded into errors: %d errors, %d refused", m.Errors, m.Refused)
	}
	if m.CallsWithoutUsage != 4 || m.CallsFromRequestModel != 7 || m.CallsWithContent != 40 {
		t.Errorf("coverage counters lost: %+v", m)
	}
}

// The summary reads the same aggregate as the table — the totals row is
// computed before the limit — so the two cannot disagree about the window.
func TestAISummaryMatchesTheModelTotals(t *testing.T) {
	cfg := ai.Config{Currency: "USD", Prices: []ai.Price{
		{Model: "gpt-4o", InputPer1MTokens: 2.5, OutputPer1MTokens: 10},
	}}
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{
			modelRow("gpt-4o", 10, 1_000_000, 1_000_000),
			modelRow("mistral-large", 5, 1_000_000, 0),
		},
		Total:      storage.AIModelUsage{Calls: 15, InputTokens: 2_000_000, OutputTokens: 1_000_000},
		ModelCount: 2,
	}}
	var resp aiSummaryResponse
	decodeInto(t, aiMux(fake, cfg), "/api/v1/ai/summary", &resp)

	if resp.Total.Calls != 15 || resp.ModelCount != 2 {
		t.Errorf("summary = %+v, want 15 calls over 2 models", resp)
	}
	if resp.TotalCost == nil || *resp.TotalCost != 12.5 {
		t.Errorf("total cost = %v, want 12.5 (a floor: mistral is unpriced)", resp.TotalCost)
	}
	if len(resp.UnpricedModels) != 1 || resp.UnpricedModels[0] != "mistral-large" {
		t.Errorf("the floor must name what it is missing: %v", resp.UnpricedModels)
	}
}

func TestAICallersPricesPerService(t *testing.T) {
	cfg := ai.Config{Currency: "USD", Prices: []ai.Price{
		{Model: "gpt-4o", InputPer1MTokens: 2.5, OutputPer1MTokens: 10},
	}}
	fake := &storagetest.Fake{AICallerRows: []storage.AICallerUsage{
		{Service: "checkout", Model: "gpt-4o", Calls: 3, InputTokens: 1_000_000, OutputTokens: 1_000_000},
		{Service: "search", Model: "llama-3", Calls: 2, InputTokens: 500_000},
	}}
	var resp aiCallersResponse
	decodeInto(t, aiMux(fake, cfg), "/api/v1/ai/callers", &resp)

	if len(resp.Callers) != 2 {
		t.Fatalf("callers = %d, want 2", len(resp.Callers))
	}
	if resp.Callers[0].Cost == nil || *resp.Callers[0].Cost != 12.5 {
		t.Errorf("checkout cost = %v, want 12.5", resp.Callers[0].Cost)
	}
	if resp.Callers[1].Cost != nil {
		t.Error("an unpriced model must not cost zero on a caller row either")
	}
}

// Empty is a legitimate answer — an install running no model calls at all —
// and it must serialize as empty arrays, never null.
func TestAIEmptyWindowIsAnEmptyAnswer(t *testing.T) {
	mux := aiMux(&storagetest.Fake{}, ai.Default())
	for _, path := range []string{"/api/v1/ai/summary", "/api/v1/ai/models", "/api/v1/ai/callers"} {
		rec := get(t, mux, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "null") {
			t.Errorf("%s: empty collections must serialize as [], got %s", path, body)
		}
	}
}
