package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/rates"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func ratesMux(fake *storagetest.Fake, chart rates.Table) *http.ServeMux {
	mux := http.NewServeMux()
	provider := func() storage.Store { return fake }
	resolver := rates.NewResolver(
		func() rates.Table { return chart },
		func() rates.Store { return fake },
	)
	Register(mux, provider, Config{Rates: resolver})
	return mux
}

func send(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type ratesBody struct {
	Overlay   rates.Table    `json:"overlay"`
	Chart     rates.Table    `json:"chart"`
	Effective rates.Resolved `json:"effective"`
	UpdatedBy string         `json:"updatedBy"`
}

func decodeRates(t *testing.T, rec *httptest.ResponseRecorder) ratesBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out ratesBody
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func chartRates() rates.Table {
	return rates.Table{
		Currency: "EUR",
		Compute:  rates.Compute{CPUCoreHour: 0.04, MemGiBHour: 0.005},
		Models:   []rates.ModelPrice{{Model: "gpt-4o", InputPer1MTokens: 2.5}},
	}
}

// A chart-declared rate is SERVED, and served as chart-owned. An operator who
// cannot see it has no way to understand why their own entry did or did not
// change anything — and would be invited to edit a value a `helm upgrade`
// silently reverts.
func TestGetRatesServesChartValuesReadOnly(t *testing.T) {
	got := decodeRates(t, send(t, ratesMux(&storagetest.Fake{}, chartRates()), http.MethodGet, "/api/v1/rates", ""))

	if len(got.Chart.Models) != 1 || got.Chart.Models[0].Model != "gpt-4o" {
		t.Errorf("chart = %+v, want the declared price", got.Chart)
	}
	if !got.Overlay.Empty() {
		t.Errorf("overlay = %+v, want empty on a fresh install", got.Overlay)
	}
	if len(got.Effective.Models) != 1 || got.Effective.Models[0].Source != rates.FromChart {
		t.Errorf("effective = %+v, want one chart-sourced model", got.Effective.Models)
	}
}

// A UI-authored entry overlays the chart, and the response says which is which.
func TestPutRatesOverlaysTheChart(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := ratesMux(fake, chartRates())

	got := decodeRates(t, send(t, mux, http.MethodPut, "/api/v1/rates",
		`{"models":[{"model":"claude-sonnet","inputPer1MTokens":3}]}`))

	if len(got.Effective.Models) != 2 {
		t.Fatalf("effective = %+v, want both the chart and the overlay model", got.Effective.Models)
	}
	bySource := map[string]rates.Provenance{}
	for _, m := range got.Effective.Models {
		bySource[m.Model] = m.Source
	}
	if bySource["gpt-4o"] != rates.FromChart || bySource["claude-sonnet"] != rates.FromOverlay {
		t.Errorf("provenance = %+v, want gpt-4o from chart and claude-sonnet from overlay", bySource)
	}
	if len(fake.SavedRatesOverlays) != 1 {
		t.Errorf("saved %d overlays, want 1", len(fake.SavedRatesOverlays))
	}
}

// A write must be visible to its own author immediately — the resolver memoizes
// for a few seconds, and an admin reading back a stale copy of their own edit
// would reasonably conclude it had not saved.
func TestPutRatesIsVisibleToItsOwnAuthorImmediately(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := ratesMux(fake, chartRates())

	// Warm the memo with the pre-write state.
	decodeRates(t, send(t, mux, http.MethodGet, "/api/v1/rates", ""))

	send(t, mux, http.MethodPut, "/api/v1/rates", `{"models":[{"model":"llama-3","inputPer1MTokens":1}]}`)

	got := decodeRates(t, send(t, mux, http.MethodGet, "/api/v1/rates", ""))
	if _, _, ok := got.Effective.Lookup("llama-3"); !ok {
		t.Error("an admin read back a stale copy of their own write")
	}
}

// The schema is CLOSED on the way in, so a setting that would never apply is
// refused rather than silently stored.
func TestPutRatesRefusesUnknownKeys(t *testing.T) {
	rec := send(t, ratesMux(&storagetest.Fake{}, rates.Table{}), http.MethodPut, "/api/v1/rates",
		`{"currency":"EUR","gpuHour":4}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestPutRatesRefusesAnUnusableTable(t *testing.T) {
	rec := send(t, ratesMux(&storagetest.Fake{}, rates.Table{}), http.MethodPut, "/api/v1/rates",
		`{"models":[{"model":"m"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (a price entry that states nothing): %s", rec.Code, rec.Body.String())
	}
}

// DELETE returns the install to whatever the chart declares — it does not
// un-price an estate that declared rates in values.
func TestDeleteRatesReturnsToTheChart(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := ratesMux(fake, chartRates())

	send(t, mux, http.MethodPut, "/api/v1/rates", `{"models":[{"model":"claude-sonnet","inputPer1MTokens":3}]}`)
	got := decodeRates(t, send(t, mux, http.MethodDelete, "/api/v1/rates", ""))

	if len(got.Effective.Models) != 1 || got.Effective.Models[0].Model != "gpt-4o" {
		t.Errorf("effective = %+v, want the chart alone", got.Effective.Models)
	}
	if got.Effective.Models[0].Source != rates.FromChart {
		t.Errorf("source = %q, want chart", got.Effective.Models[0].Source)
	}
}

// The AI screen prices through the SAME table, so a UI-authored price shows up
// there without a restart. This is the seam the whole change exists to close.
func TestAIModelsPriceThroughTheRateTable(t *testing.T) {
	fake := &storagetest.Fake{AIUsageResult: storage.AIUsage{
		Models: []storage.AIModelUsage{modelRow("gpt-4o", 1, 1_000_000, 0)},
		Total:  modelRow("", 1, 1_000_000, 0),
	}}
	mux := ratesMux(fake, rates.Table{})

	send(t, mux, http.MethodPut, "/api/v1/rates",
		`{"currency":"EUR","models":[{"model":"gpt-4o","inputPer1MTokens":7}]}`)

	var resp struct {
		Models []struct {
			Model string   `json:"model"`
			Cost  *float64 `json:"cost"`
		} `json:"models"`
		Priced   bool   `json:"priced"`
		Currency string `json:"currency"`
	}
	decodeInto(t, mux, "/api/v1/ai/models", &resp)

	if !resp.Priced || resp.Currency != "EUR" {
		t.Errorf("priced=%v currency=%q, want a priced EUR install", resp.Priced, resp.Currency)
	}
	if len(resp.Models) != 1 || resp.Models[0].Cost == nil || *resp.Models[0].Cost != 7 {
		t.Errorf("cost = %+v, want 7 from the UI-authored rate", resp.Models)
	}
}
