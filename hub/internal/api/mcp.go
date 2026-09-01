package api

import (
	"io"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/mcp"
)

// maxMCPBody bounds a JSON-RPC request. Tool arguments are filters — a service
// name, a window, a row limit — so the 64 KiB every other JSON endpoint here
// uses is orders of magnitude more than an honest call needs.
const maxMCPBody = 1 << 16

// handleMCP serves one Model Context Protocol request.
//
// Everything about WHO is asking has already happened by the time this runs:
// secured() resolved the credential (a personal API token, or a session),
// refused anyone without Viewer, and projectTenants below authorizes the
// project and expands it to the tenants the read may span. The mcp package
// makes no authorization decision of its own — which is exactly what keeps
// this client on the one permission model instead of growing a parallel copy
// that drifts (design/2026-08-13-api-tokens.md).
func (a *API) handleMCP(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		return err // *http.MaxBytesError → 413, handled centrally
	}
	srv := &mcp.Server{
		Store:   store,
		Modules: a.modules,
		Tenant:  tenant,
		Tenants: tenants,
		// The same accessor the service map uses, reading the same
		// hot-reloaded config: one source, so the map and the tool cannot
		// disagree about which workload is a proxy.
		Topology: a.topologyClassifier(),
		Version:  Version,
		Actor:    actorName(r),
	}
	reply, err := srv.Handle(r.Context(), body)
	if err != nil {
		return err
	}
	if reply == nil {
		// A notification: delivered, not answered.
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
	return nil
}

// actorName is who the audit line names. Empty when auth is off, which the
// audit line reports as such rather than inventing a user.
func actorName(r *http.Request) string {
	id := identityFrom(r.Context())
	if id == nil {
		return ""
	}
	if id.Email != "" {
		return id.Email
	}
	return id.UserID
}
