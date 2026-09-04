package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// maxRegistrationBody bounds the registration request, the same way the tokens
// endpoint bounds its own.
const maxRegistrationBody = 1 << 16 // 64 KiB

// handleRegisterClient implements RFC 7591 dynamic client registration.
//
// Unauthenticated by necessity: a claude.ai connector has no credential for
// this install and no way to be pre-registered. What makes that acceptable is
// that registering grants NOTHING — the row it writes cannot read a single
// span until a person signs in and consents to it by name.
//
// Everything the client declares about itself is stored verbatim and treated as
// unverified for the rest of its life. The consent screen says so.
func (a *API) handleRegisterClient(w http.ResponseWriter, r *http.Request) error {
	if !a.cfg.OAuthDynamicRegistration {
		return &apiError{status: http.StatusNotFound, message: "dynamic client registration is disabled on this install"}
	}
	if !a.registrar.Allow(clientIP(r)) {
		return &apiError{status: http.StatusTooManyRequests, message: "too many registrations from this address"}
	}

	var req oauth.Registration
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegistrationBody))
	if err := dec.Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	reg, oerr := oauth.ValidateRegistration(req)
	if oerr != nil {
		return writeOAuthError(http.StatusBadRequest, oerr)
	}

	st, err := a.store()
	if err != nil {
		return err
	}
	now := time.Now()
	client := storage.OAuthClient{
		ClientID:        auth.NewID(),
		Name:            reg.ClientName,
		RedirectURIs:    reg.RedirectURIs,
		GrantTypes:      reg.GrantTypes,
		TokenAuthMethod: reg.TokenEndpointAuthMethod,
		Scope:           reg.Scope,
		SoftwareID:      reg.SoftwareID,
		ClientURI:       reg.ClientURI,
		LogoURI:         reg.LogoURI,
		// Recorded so an operator can answer "where did this registration come
		// from" — the one thing about a self-declared client that is not
		// self-declared.
		RegisteredIP: clientIP(r),
		CreatedAt:    now,
	}
	if err := st.CreateOAuthClient(r.Context(), client); err != nil {
		return err
	}

	writeJSON(w, http.StatusCreated, oauth.RegistrationResponse{
		ClientID:                client.ClientID,
		ClientIDIssuedAt:        now.Unix(),
		ClientName:              client.Name,
		RedirectURIs:            client.RedirectURIs,
		GrantTypes:              client.GrantTypes,
		ResponseTypes:           reg.ResponseTypes,
		TokenEndpointAuthMethod: client.TokenAuthMethod,
		Scope:                   client.Scope,
	})
	return nil
}

// clientIP is the peer address, preferring the proxy's forwarded value because
// the hub normally sits behind one. Used ONLY for rate limiting and for the
// audit record of where a registration came from — never for authorization, so
// a spoofed header buys nothing but a different rate-limit bucket.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i > 0 {
			fwd = fwd[:i]
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// oauthErrorBody is the RFC 6749 error shape. Distinct from the hub's own
// apiError body because an OAuth client parses these fields by name.
type oauthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func writeOAuthError(status int, e *oauth.Error) error {
	return &oauthAPIError{status: status, err: e}
}

type oauthAPIError struct {
	status int
	err    *oauth.Error
}

func (e *oauthAPIError) Error() string { return e.err.Error() }
