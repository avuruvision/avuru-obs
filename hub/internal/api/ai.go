package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/ai"
	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The AI-observability read surface (module ai). Three routes over one storage
// read each, all taking the trace filter vocabulary so their numbers reconcile
// with the Traces screen. See design/2026-08-27-ai-observability.md.

// aiConfig resolves the current prices, defaulting to none — which the
// responses report as "not priced", never as free.
func (a *API) aiConfig() ai.Config {
	if a.cfg.AIConfig != nil {
		return a.cfg.AIConfig()
	}
	return ai.Default()
}

// aiUsageDTO is the shape every AI row shares: what was called, how it went,
// and what it spent. It carries the counters that keep the screen honest
// rather than leaving the client to infer them from zeroes.
type aiUsageDTO struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`

	Calls   uint64 `json:"calls"`
	Errors  uint64 `json:"errors"`
	Refused uint64 `json:"refused"`
	// Truncated is a successful call the model cut off at the token ceiling.
	// Reported apart from errors on purpose: the request worked, and the
	// answer may still be unusable.
	Truncated uint64 `json:"truncated"`

	// CallsWithoutUsage is the population EXCLUDED from the token totals — a
	// call that reported no usage is not a call that used nothing.
	CallsWithoutUsage uint64 `json:"callsWithoutUsage"`
	// CallsFromRequestModel counts rows attributed to the model that was
	// asked for, because nothing said what answered.
	CallsFromRequestModel uint64 `json:"callsFromRequestModel"`
	// CallsWithContent counts calls still carrying prompt or completion text.
	// Never rendered — reported, so an operator can discover an exposure the
	// product did not create and cannot otherwise show them.
	CallsWithContent uint64 `json:"callsWithContent"`

	InputTokens  uint64 `json:"inputTokens"`
	OutputTokens uint64 `json:"outputTokens"`

	P50Ms float64 `json:"p50Ms"`
	P95Ms float64 `json:"p95Ms"`
	P99Ms float64 `json:"p99Ms"`

	// Cost is present only when this model has a price. Absent means "no rate
	// declared", which is a different statement from zero.
	Cost *float64 `json:"cost,omitempty"`
	// PricedByPrefix marks a cost derived from a prefix rule rather than from
	// an entry naming this exact model — a number the product inferred, not
	// one the operator stated.
	PricedByPrefix bool `json:"pricedByPrefix,omitempty"`
}

type aiModelsResponse struct {
	Models []aiUsageDTO `json:"models"`
	// Total covers EVERY model call in the window, including models past the
	// limit, so a truncated table still sits under a whole-window summary.
	Total aiUsageDTO `json:"total"`
	// Other is the tail: the window minus the models listed. Absent when the
	// table is complete. Its quantiles are zero — latency cannot be recovered
	// by subtraction — and the client must not draw them.
	Other *aiUsageDTO `json:"other,omitempty"`
	// ModelCount is the DISTINCT model count over the window, which exceeds
	// len(models) exactly when the limit bit.
	ModelCount uint64 `json:"modelCount"`
	Priced     bool   `json:"priced"`
	Currency   string `json:"currency,omitempty"`
	// UnpricedModels names the listed models with no rate, so the screen can
	// say why a cost column has gaps instead of showing zeros.
	UnpricedModels []string `json:"unpricedModels"`
}

type aiSummaryResponse struct {
	Total      aiUsageDTO `json:"total"`
	ModelCount uint64     `json:"modelCount"`
	Priced     bool       `json:"priced"`
	Currency   string     `json:"currency,omitempty"`
	// TotalCost is the sum of the PRICED models' costs, so it is a floor
	// whenever UnpricedModels is non-empty. Absent when nothing is priced.
	TotalCost *float64 `json:"totalCost,omitempty"`
	// UnpricedModels names the models the total could not include.
	UnpricedModels []string `json:"unpricedModels"`
}

type aiCallerDTO struct {
	Service      string   `json:"service"`
	Model        string   `json:"model"`
	Calls        uint64   `json:"calls"`
	Errors       uint64   `json:"errors"`
	Truncated    uint64   `json:"truncated"`
	InputTokens  uint64   `json:"inputTokens"`
	OutputTokens uint64   `json:"outputTokens"`
	Cost         *float64 `json:"cost,omitempty"`
}

type aiCallersResponse struct {
	Callers  []aiCallerDTO `json:"callers"`
	Priced   bool          `json:"priced"`
	Currency string        `json:"currency,omitempty"`
}

// aiQuery builds the storage query from the shared filter parameters.
func (a *API) aiQuery(r *http.Request, defaultLimit int) (storage.AIQuery, error) {
	tr, err := parseTimeRange(r)
	if err != nil {
		return storage.AIQuery{}, err
	}
	limit, err := parseInt(r, "limit", defaultLimit)
	if err != nil {
		return storage.AIQuery{}, err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return storage.AIQuery{}, err
	}
	return storage.AIQuery{
		Tenant:     tenant,
		Tenants:    tenants,
		Range:      tr,
		Service:    r.URL.Query().Get("service"),
		Model:      r.URL.Query().Get("model"),
		Tags:       parseTags(r),
		ExcludeAux: !parseBool(r, "includeAux", false),
		Limit:      limit,
	}, nil
}

// toAIUsage converts one storage row, pricing it when a rate applies.
func toAIUsage(m storage.AIModelUsage, cfg ai.Config) aiUsageDTO {
	d := aiUsageDTO{
		Model:                 m.Model,
		Provider:              m.Provider,
		Calls:                 m.Calls,
		Errors:                m.Errors,
		Refused:               m.Refused,
		Truncated:             m.Truncated,
		CallsWithoutUsage:     m.CallsWithoutUsage,
		CallsFromRequestModel: m.CallsFromRequestModel,
		CallsWithContent:      m.CallsWithContent,
		InputTokens:           m.InputTokens,
		OutputTokens:          m.OutputTokens,
		P50Ms:                 ms(m.P50),
		P95Ms:                 ms(m.P95),
		P99Ms:                 ms(m.P99),
	}
	if p, byPrefix, ok := cfg.Lookup(m.Model); ok {
		cost := p.Cost(m.InputTokens, m.OutputTokens)
		d.Cost = &cost
		d.PricedByPrefix = byPrefix
	}
	return d
}

// handleAIModels returns per-model usage over the window, with the totals and
// an honest tail.
func (a *API) handleAIModels(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	q, err := a.aiQuery(r, 50)
	if err != nil {
		return err
	}
	usage, err := store.AIModels(r.Context(), q)
	if err != nil {
		return err
	}

	cfg := a.aiConfig()
	resp := aiModelsResponse{
		Models:         []aiUsageDTO{},
		Total:          toAIUsage(usage.Total, cfg),
		ModelCount:     usage.ModelCount,
		Priced:         cfg.Priced(),
		UnpricedModels: []string{},
	}
	// The totals row has no model, so it never matches a price. Its cost is
	// the sum of the priced models' costs instead — a floor, and the response
	// names what the floor is missing.
	resp.Total.Cost = nil
	resp.Total.PricedByPrefix = false
	if cfg.Priced() {
		resp.Currency = cfg.Currency
	}

	var shown storage.AIModelUsage
	var pricedTotal float64
	var anyPriced bool
	for _, m := range usage.Models {
		d := toAIUsage(m, cfg)
		if d.Cost != nil {
			pricedTotal += *d.Cost
			anyPriced = true
		} else if cfg.Priced() {
			resp.UnpricedModels = append(resp.UnpricedModels, m.Model)
		}
		resp.Models = append(resp.Models, d)
		shown = addAIUsage(shown, m)
	}
	if anyPriced {
		total := pricedTotal
		resp.Total.Cost = &total
	}

	// The tail, derived from the totals rather than queried again. Quantiles
	// stay zero: a p95 cannot be subtracted.
	if usage.Total.Calls > shown.Calls {
		other := toAIUsage(subAIUsage(usage.Total, shown), cfg)
		other.Model = ""
		other.Cost, other.PricedByPrefix = nil, false
		resp.Other = &other
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleAISummary is the window as one line. It reads the same aggregate as
// the model table — the totals row is computed before the limit — so the two
// screens cannot disagree about how many calls the window held.
func (a *API) handleAISummary(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	q, err := a.aiQuery(r, 200)
	if err != nil {
		return err
	}
	usage, err := store.AIModels(r.Context(), q)
	if err != nil {
		return err
	}

	cfg := a.aiConfig()
	resp := aiSummaryResponse{
		Total:          toAIUsage(usage.Total, cfg),
		ModelCount:     usage.ModelCount,
		Priced:         cfg.Priced(),
		UnpricedModels: []string{},
	}
	resp.Total.Cost = nil
	resp.Total.PricedByPrefix = false
	if cfg.Priced() {
		resp.Currency = cfg.Currency
	}

	var total float64
	var anyPriced bool
	for _, m := range usage.Models {
		p, _, ok := cfg.Lookup(m.Model)
		if !ok {
			if cfg.Priced() {
				resp.UnpricedModels = append(resp.UnpricedModels, m.Model)
			}
			continue
		}
		total += p.Cost(m.InputTokens, m.OutputTokens)
		anyPriced = true
	}
	if anyPriced {
		t := total
		resp.TotalCost = &t
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleAICallers reports usage per calling service and model — spend with an
// owner.
func (a *API) handleAICallers(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	q, err := a.aiQuery(r, 100)
	if err != nil {
		return err
	}
	rows, err := store.AICallers(r.Context(), q)
	if err != nil {
		return err
	}

	cfg := a.aiConfig()
	resp := aiCallersResponse{Callers: []aiCallerDTO{}, Priced: cfg.Priced()}
	if cfg.Priced() {
		resp.Currency = cfg.Currency
	}
	for _, c := range rows {
		d := aiCallerDTO{
			Service:      c.Service,
			Model:        c.Model,
			Calls:        c.Calls,
			Errors:       c.Errors,
			Truncated:    c.Truncated,
			InputTokens:  c.InputTokens,
			OutputTokens: c.OutputTokens,
		}
		if p, _, ok := cfg.Lookup(c.Model); ok {
			cost := p.Cost(c.InputTokens, c.OutputTokens)
			d.Cost = &cost
		}
		resp.Callers = append(resp.Callers, d)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// addAIUsage accumulates the counters of the rows actually shown, so the tail
// can be derived from the totals. Quantiles are not summable and are left out.
func addAIUsage(acc, m storage.AIModelUsage) storage.AIModelUsage {
	acc.Calls += m.Calls
	acc.Errors += m.Errors
	acc.Refused += m.Refused
	acc.Truncated += m.Truncated
	acc.CallsWithoutUsage += m.CallsWithoutUsage
	acc.CallsFromRequestModel += m.CallsFromRequestModel
	acc.CallsWithContent += m.CallsWithContent
	acc.InputTokens += m.InputTokens
	acc.OutputTokens += m.OutputTokens
	return acc
}

// subAIUsage is the tail: everything the window held minus what was listed.
// Saturating, because a total that somehow trails its parts must produce an
// empty tail rather than an enormous one.
func subAIUsage(total, shown storage.AIModelUsage) storage.AIModelUsage {
	return storage.AIModelUsage{
		Calls:                 saturatingSub(total.Calls, shown.Calls),
		Errors:                saturatingSub(total.Errors, shown.Errors),
		Refused:               saturatingSub(total.Refused, shown.Refused),
		Truncated:             saturatingSub(total.Truncated, shown.Truncated),
		CallsWithoutUsage:     saturatingSub(total.CallsWithoutUsage, shown.CallsWithoutUsage),
		CallsFromRequestModel: saturatingSub(total.CallsFromRequestModel, shown.CallsFromRequestModel),
		CallsWithContent:      saturatingSub(total.CallsWithContent, shown.CallsWithContent),
		InputTokens:           saturatingSub(total.InputTokens, shown.InputTokens),
		OutputTokens:          saturatingSub(total.OutputTokens, shown.OutputTokens),
	}
}
