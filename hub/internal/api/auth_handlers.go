package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
)

type authConfigResponse struct {
	Enabled  bool     `json:"enabled"`
	Methods  []string `json:"methods"`
	ForceSSO bool     `json:"forceSSO"`
}

// handleAuthConfig is unauthenticated and ALWAYS registered (auth on or
// off): the login page needs a straight answer, not a 404 it has to
// special-case. Enabled implies ["local"]; when an OIDC provider is wired
// (discovery succeeded) "oidc" is appended and ForceSSO reflects the config.
func (a *API) handleAuthConfig(w http.ResponseWriter, _ *http.Request) error {
	resp := authConfigResponse{Enabled: a.cfg.Auth != nil, Methods: []string{}}
	if resp.Enabled {
		resp.Methods = []string{"local"}
		if a.cfg.OIDC != nil && a.cfg.OIDC() != nil {
			resp.Methods = append(resp.Methods, "oidc")
			resp.ForceSSO = a.oidcForceSSO()
		}
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// oidcForceSSO reports the current OIDC forceSSO setting, nil-safe against an
// unwired accessor or a nil (unconfigured) settings value.
func (a *API) oidcForceSSO() bool {
	if a.cfg.OIDCSettings == nil {
		return false
	}
	if s := a.cfg.OIDCSettings(); s != nil {
		return s.ForceSSO
	}
	return false
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type meResponse struct {
	User struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		Anonymous bool   `json:"anonymous"`
	} `json:"user"`
	Grants []auth.Grant `json:"grants"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) error {
	// Credentialed/identity responses must never be cached (OWASP: a shared
	// or misconfigured cache serving one user's session state to another).
	w.Header().Set("Cache-Control", "no-store")
	if err := checkOrigin(r); err != nil {
		return err
	}
	// The one unauthenticated POST route — cap the body like the other
	// decode sites (alerts.go, error_tracking.go) so an unauthenticated
	// caller can't hand us an unbounded body.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return decodeJSONError(err) // oversized body → 413, else 400
	}
	if req.Email == "" || req.Password == "" {
		return badRequest("email and password required")
	}
	// RemoteAddr, deliberately NOT X-Forwarded-For: a spoofable header would
	// be an unlimited rate-limit bypass (fresh limiter key per forged IP).
	// Behind an ingress all clients share the proxy IP, so the limiter
	// degenerates to per-email — standard, and the safer failure mode.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, id, err := a.cfg.Auth.Login(r.Context(), req.Email, req.Password, ip)
	switch {
	case errors.Is(err, auth.ErrStoreUnavailable):
		// Consistent with requestIdentity — a store blip reads as 503, not
		// the generic 500 handle() would otherwise map this to.
		return errStoreUnavailable
	case errors.Is(err, auth.ErrTooManyAttempts):
		// Header written before handle() writes the body.
		w.Header().Set("Retry-After", "60")
		return &apiError{status: http.StatusTooManyRequests, message: "too many attempts, retry in a minute"}
	case errors.Is(err, auth.ErrInvalidCredentials):
		return unauthorized()
	case err != nil:
		return err
	}
	// Cookie Max-Age mirrors the server-side TTL — single source of truth.
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	writeJSON(w, http.StatusOK, meFrom(id))
	return nil
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) error {
	// Credentialed/identity responses must never be cached (OWASP).
	w.Header().Set("Cache-Control", "no-store")
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		if err := a.cfg.Auth.Logout(r.Context(), c.Value); err != nil {
			if errors.Is(err, auth.ErrStoreUnavailable) {
				// Consistent with requestIdentity — a store blip reads as
				// 503, not the generic 500 handle() would otherwise map this to.
				return errStoreUnavailable
			}
			return err
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) error {
	// Credentialed/identity responses must never be cached (OWASP).
	w.Header().Set("Cache-Control", "no-store")
	id := identityFrom(r.Context())
	if id == nil { // unreachable while the route requires auth; defensive
		return unauthorized()
	}
	writeJSON(w, http.StatusOK, meFrom(*id))
	return nil
}

func meFrom(id auth.Identity) meResponse {
	var resp meResponse
	resp.User.ID = id.UserID
	resp.User.Email = id.Email
	resp.User.Name = id.Name
	resp.User.Anonymous = id.Anonymous
	resp.Grants = id.Grants
	if resp.Grants == nil {
		resp.Grants = []auth.Grant{} // wire shape: [] not null
	}
	return resp
}
