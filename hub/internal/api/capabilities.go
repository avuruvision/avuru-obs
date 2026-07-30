package api

import "net/http"

// capabilitiesResponse is the client-agnostic module-discovery contract: the
// SPA builds its sidebar from it, and future clients (Grafana, CLI) use it to
// know which signal APIs exist on this install.
type capabilitiesResponse struct {
	Version string   `json:"version"`
	Modules []string `json:"modules"`
	// CollectionRuntimeControl tells the SPA whether /api/v1/collection/overlay
	// exists on this install, so it can hide the collection editor instead of
	// discovering the routes are 404 (design/2026-07-27-collection-control-plane.md).
	CollectionRuntimeControl bool `json:"collectionRuntimeControl"`
}

func (a *API) handleCapabilities(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		Version:                  Version,
		Modules:                  a.modules.Names(),
		CollectionRuntimeControl: a.cfg.CollectionRuntimeControlEnabled,
	})
	return nil
}
