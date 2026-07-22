package api

import "net/http"

// The green module's HTTP surface. The routes register only when the module
// is active (the 404-when-off contract); the energy/carbon read path is not
// built yet, so each handler answers 501 through the central error wrapper.
// See design/2026-07-22-green-carbon.md.

var errGreenNotImplemented = &apiError{status: http.StatusNotImplemented, message: "green endpoints are not implemented yet"}

// handleGreenSummary will report per-service energy (Wh) and carbon (gCO2e)
// over the requested window.
func (a *API) handleGreenSummary(_ http.ResponseWriter, _ *http.Request) error {
	return errGreenNotImplemented
}

// handleGreenBudgets will report carbon-budget status per service group.
func (a *API) handleGreenBudgets(_ http.ResponseWriter, _ *http.Request) error {
	return errGreenNotImplemented
}

// handleGreenReport will produce the CSRD export with its methodology block.
func (a *API) handleGreenReport(_ http.ResponseWriter, _ *http.Request) error {
	return errGreenNotImplemented
}
