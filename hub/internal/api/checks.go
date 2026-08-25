package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// checkDTO is one declared check and what the last probe found.
type checkDTO struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Tier     string `json:"tier"`
	URL      string `json:"url"`
	Interval string `json:"interval"`
}

type checksResponse struct {
	Checks []checkDTO `json:"checks"`
}

type checkResultDTO struct {
	At        string  `json:"at"`
	OK        bool    `json:"ok"`
	Status    int     `json:"status,omitempty"`
	LatencyMs float64 `json:"latencyMs"`
	Error     string  `json:"error,omitempty"`
	// The probe's own span. Present when a gateway endpoint is configured; it
	// is the click-through from "this check failed" to "here is why".
	TraceID string `json:"traceId,omitempty"`
}

type checkResultsResponse struct {
	CheckID string           `json:"checkId"`
	Results []checkResultDTO `json:"results"`
}

// handleChecks lists the declared checks. Configuration, not measurement — the
// outcomes ride the health board (each group carries its checks' standing) and
// the per-check history is the route below.
func (a *API) handleChecks(w http.ResponseWriter, r *http.Request) error {
	cfg := a.groupsConfig(r.Context())
	resp := checksResponse{Checks: []checkDTO{}}
	for _, gc := range cfg.AllChecks() {
		interval, err := gc.Check.IntervalOrDefault()
		if err != nil {
			continue // rejected at load; nothing sensible to report
		}
		resp.Checks = append(resp.Checks, checkDTO{
			ID:       gc.Check.ID,
			Group:    gc.Group,
			Tier:     string(gc.Tier),
			URL:      gc.Check.URL,
			Interval: interval.String(),
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleCheckResults returns one check's recent outcomes, newest first.
func (a *API) handleCheckResults(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	// A 404 for an id that was never declared, rather than an empty list: the
	// difference between "this check has not run" and "there is no such check"
	// is the difference between waiting and fixing a typo.
	known := false
	for _, gc := range a.groupsConfig(r.Context()).AllChecks() {
		if gc.Check.ID == id {
			known = true
			break
		}
	}
	if !known {
		return storage.ErrNotFound
	}

	limit, err := parseInt(r, "limit", 100)
	if err != nil {
		return err
	}
	results, err := store.CheckResults(r.Context(), storage.CheckQuery{
		Tenant: tenant, Tenants: tenants, CheckID: id, Limit: limit,
	})
	if err != nil {
		return err
	}
	resp := checkResultsResponse{CheckID: id, Results: make([]checkResultDTO, 0, len(results))}
	for _, res := range results {
		resp.Results = append(resp.Results, checkResultDTO{
			At:        res.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
			OK:        res.OK,
			Status:    res.Status,
			LatencyMs: res.LatencyMs,
			Error:     res.Error,
			TraceID:   res.TraceID,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
