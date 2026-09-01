package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/rates"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// maxRatesBody bounds the rate-table request body. A price list is a handful of
// small objects; 64 KiB is already generous, and the cap is what stops an
// unbounded read.
const maxRatesBody = 1 << 16

type ratesResponse struct {
	// Overlay is what the UI authored — the only part a PUT replaces.
	Overlay rates.Table `json:"overlay"`
	// Chart is what the install declares in values, served READ-ONLY. Sent so
	// the screen can show it greyed rather than pretending it does not exist:
	// an operator who cannot see a chart-declared price has no way to
	// understand why their own entry did or did not change anything.
	Chart rates.Table `json:"chart"`
	// Effective is the merged result — what actually prices things.
	Effective rates.Resolved `json:"effective"`
	UpdatedAt string         `json:"updatedAt,omitempty"`
	UpdatedBy string         `json:"updatedBy,omitempty"`
}

// handleGetRates returns the UI-authored overlay, the chart-declared table it
// sits on, and the merged result. "Never saved" is not an error — it is the
// empty overlay, i.e. exactly what the chart declares.
func (a *API) handleGetRates(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	resp := ratesResponse{
		Chart:     a.rates.Declared(),
		Effective: a.rates.Resolve(r.Context()),
	}
	rec, err := store.LoadRatesOverlay(r.Context())
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, resp)
		return nil
	}
	if err != nil {
		return err
	}
	ov, err := rates.ParseOverlay([]byte(rec.Overlay))
	if err != nil {
		// A stored row that fails to parse is a server-side bug (it passed
		// validation on the way in), not a client error.
		return fmt.Errorf("parse stored rates overlay: %w", err)
	}
	resp.Overlay = ov
	resp.UpdatedBy = rec.UpdatedBy
	if !rec.UpdatedAt.IsZero() {
		resp.UpdatedAt = rec.UpdatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handlePutRates replaces the overlay wholesale. There is no PATCH merge: the
// UI always sends the full desired state, which is the only way a deletion can
// be expressed unambiguously.
func (a *API) handlePutRates(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	t, err := readRatesBody(w, r)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode rates overlay: %w", err)
	}
	if err := store.SaveRatesOverlay(r.Context(), storage.RatesOverlay{
		Overlay:   string(encoded),
		UpdatedBy: requestedBy(r),
	}); err != nil {
		return err
	}
	// The resolver memoizes for a few seconds; an admin must never read back
	// their own write as stale.
	a.rates.Invalidate()

	writeJSON(w, http.StatusOK, ratesResponse{
		Overlay:   t,
		Chart:     a.rates.Declared(),
		Effective: a.rates.Resolve(r.Context()),
	})
	return nil
}

// handleDeleteRates drops the overlay, returning the install to whatever the
// chart declares. Saving the empty table rather than deleting the row keeps the
// singleton idiom and the audit trail (UpdatedBy) intact.
func (a *API) handleDeleteRates(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	if err := store.SaveRatesOverlay(r.Context(), storage.RatesOverlay{
		Overlay:   "{}",
		UpdatedBy: requestedBy(r),
	}); err != nil {
		return err
	}
	a.rates.Invalidate()

	writeJSON(w, http.StatusOK, ratesResponse{
		Chart:     a.rates.Declared(),
		Effective: a.rates.Resolve(r.Context()),
	})
	return nil
}

func readRatesBody(w http.ResponseWriter, r *http.Request) (rates.Table, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRatesBody))
	if err != nil {
		return rates.Table{}, decodeJSONError(err)
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return rates.Table{}, badRequest("empty request body")
	}
	t, err := rates.ParseOverlay([]byte(body))
	if err != nil {
		return rates.Table{}, badRequest("%s", err)
	}
	return t, nil
}
