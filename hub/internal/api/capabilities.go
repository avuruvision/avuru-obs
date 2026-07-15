package api

import "net/http"

// capabilitiesResponse is the client-agnostic module-discovery contract: the
// SPA builds its sidebar from it, and future clients (Grafana, CLI) use it to
// know which signal APIs exist on this install.
type capabilitiesResponse struct {
	Version string   `json:"version"`
	Modules []string `json:"modules"`
}

func (a *API) handleCapabilities(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, capabilitiesResponse{Version: Version, Modules: a.modules.Names()})
	return nil
}
