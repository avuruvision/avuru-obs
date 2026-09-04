package api

import (
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/oauth"
)

// The two discovery documents, served UNAUTHENTICATED at the origin root.
//
// Unauthenticated is required, not an oversight: a client fetches these before
// it has any credential — discovering where to get one is their entire purpose.
// They carry no tenant names, no user counts and nothing this install would not
// already tell an anonymous caller; see the metadata builders in
// hub/internal/oauth for exactly what goes in them.
//
// They must live at the ORIGIN ROOT per RFC 8414/9728, which is why they are
// registered outside /api/v1 alongside /mcp itself.

func (a *API) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, oauth.NewProtectedResource(a.cfg.PublicURL))
}

func (a *API) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK,
		oauth.NewAuthorizationServer(a.cfg.PublicURL, a.cfg.OAuthDynamicRegistration))
}
