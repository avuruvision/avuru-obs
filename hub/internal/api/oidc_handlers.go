package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
)

const (
	oidcStateCookie    = "avuru_oidc_state"
	oidcNonceCookie    = "avuru_oidc_nonce"
	oidcVerifierCookie = "avuru_oidc_verifier"
)

// oidcProvider resolves the current OIDC provider, nil-safe against an unwired
// accessor (OIDC off) or a nil provider (discovery not yet succeeded).
func (a *API) oidcProvider() *auth.OIDCProvider {
	if a.cfg.OIDC == nil {
		return nil
	}
	return a.cfg.OIDC()
}

// handleOIDCStart begins the auth-code flow: random state+nonce in short-lived
// HttpOnly cookies + a PKCE verifier, then 302 to the IdP.
func (a *API) handleOIDCStart(w http.ResponseWriter, r *http.Request) error {
	p := a.oidcProvider()
	if p == nil {
		return badRequest("OIDC is not configured")
	}
	state, nonce := auth.NewID(), auth.NewID()
	start := p.Start(state, nonce)
	setShortCookie(w, r, oidcStateCookie, state)
	setShortCookie(w, r, oidcNonceCookie, nonce)
	setShortCookie(w, r, oidcVerifierCookie, start.Verifier)
	http.Redirect(w, r, start.URL, http.StatusFound)
	return nil
}

// handleOIDCCallback validates state, exchanges the code, starts a session.
func (a *API) handleOIDCCallback(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	p := a.oidcProvider()
	if p == nil {
		return badRequest("OIDC is not configured")
	}
	state, err := r.Cookie(oidcStateCookie)
	if err != nil || state.Value == "" || state.Value != r.URL.Query().Get("state") {
		return forbidden("invalid or missing OIDC state")
	}
	nonce, _ := r.Cookie(oidcNonceCookie)
	verifier, _ := r.Cookie(oidcVerifierCookie)
	ext, err := p.Exchange(r.Context(), r.URL.Query().Get("code"), cookieVal(verifier), cookieVal(nonce))
	if err != nil {
		return unauthorized()
	}
	token, _, err := a.cfg.Auth.CompleteSSO(r.Context(), ext)
	if err != nil {
		return err
	}
	clearShortCookie(w, r, oidcStateCookie)
	clearShortCookie(w, r, oidcNonceCookie)
	clearShortCookie(w, r, oidcVerifierCookie)
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	http.Redirect(w, r, "/", http.StatusFound)
	return nil
}
