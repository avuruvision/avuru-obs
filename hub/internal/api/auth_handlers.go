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
	Enabled     bool     `json:"enabled"`
	Methods     []string `json:"methods"`
	ForceSSO    bool     `json:"forceSSO"`
	DemoEnabled bool     `json:"demoEnabled"`
}

// handleAuthConfig is unauthenticated and ALWAYS registered (auth on or
// off): the login page needs a straight answer, not a 404 it has to
// special-case. Enabled implies ["local"]; when an OIDC provider is wired
// (discovery succeeded) "oidc" is appended and ForceSSO reflects the config.
func (a *API) handleAuthConfig(w http.ResponseWriter, _ *http.Request) error {
	resp := authConfigResponse{Enabled: a.cfg.Auth != nil, Methods: []string{}, DemoEnabled: a.cfg.DemoEnabled}
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

// handleDemoLogin signs the caller in as the read-only demo viewer using the
// server-held demo credentials — the shared password never reaches the browser.
// Registered only when DemoEnabled. Reuses the login rate limiter (keyed on the
// demo email + client IP).
func (a *API) handleDemoLogin(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "no-store")
	if err := a.checkOrigin(r); err != nil {
		return err
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, id, err := a.cfg.Auth.Login(r.Context(), a.cfg.DemoEmail, a.cfg.DemoPassword, ip)
	switch {
	case errors.Is(err, auth.ErrStoreUnavailable):
		return errStoreUnavailable
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		return &apiError{status: http.StatusTooManyRequests, message: "too many attempts, retry in a minute"}
	case err != nil:
		// Any other failure (e.g. the demo user isn't bootstrapped yet) is a
		// server-side misconfiguration, not a client error.
		return err
	}
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	writeJSON(w, http.StatusOK, meFrom(id))
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
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		// Origin ("local" | "oidc"; empty for the anonymous identity) tells
		// the SPA whether a password form applies at all — an SSO user's
		// credential lives at the IdP, so the Account tab shows a note instead.
		Origin    string `json:"origin"`
		Anonymous bool   `json:"anonymous"`
	} `json:"user"`
	Grants []auth.Grant `json:"grants"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) error {
	// Credentialed/identity responses must never be cached (OWASP: a shared
	// or misconfigured cache serving one user's session state to another).
	w.Header().Set("Cache-Control", "no-store")
	if err := a.checkOrigin(r); err != nil {
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

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// handleChangePassword is the self-service half of password management
// (design/2026-08-06-users-crud-password.md): any signed-in local user
// rotates their OWN password after proving they hold the current one. The
// admin half lives in handleUpdateUser. Wrong-current answers 400, not 401 —
// the SPA treats 401 as session-expired and would bounce the user to login
// mid-form. Success rotates the session cookie in place.
//
// The rotated user is always the authenticated one: ChangePassword's userID
// comes from the session identity and this request type carries no user
// field, which is what its doc comment requires (a caller free to name any
// user could enumerate accounts by status and by response time alike).
//
// CSRF is the authenticated() wrapper's job — it runs checkOrigin for every
// route registered through it, so this handler does not repeat it (only
// handleLogin does, being registered with bare handle()).
func (a *API) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	// Credentialed responses must never be cached (OWASP).
	w.Header().Set("Cache-Control", "no-store")
	id := identityFrom(r.Context())
	if id == nil || id.Anonymous {
		return unauthorized()
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return badRequest("current and new password required")
	}
	// RemoteAddr for the limiter, same reasoning as handleLogin.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, err := a.cfg.Auth.ChangePassword(r.Context(), id.UserID, req.CurrentPassword, req.NewPassword, ip)
	switch {
	case errors.Is(err, auth.ErrStoreUnavailable):
		return errStoreUnavailable
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		return &apiError{status: http.StatusTooManyRequests, message: "too many attempts, retry in a minute"}
	case errors.Is(err, auth.ErrExternalPassword):
		return badRequest("your password is managed by the identity provider")
	case errors.Is(err, auth.ErrDemoUser):
		return &apiError{status: http.StatusForbidden, message: "the shared demo account cannot change its password"}
	// A caller error about the NEW password (bcrypt refuses >72 bytes; the
	// auth layer also returns it for an empty one), so a 400 — never the 500
	// an unmapped sentinel would fall through to. It must not share the
	// wrong-current message either: that is the whole reason the sentinel
	// exists, and the two send the user to different fields.
	case errors.Is(err, auth.ErrPasswordUnusable):
		return badRequest("that new password cannot be used (passwords are limited to 72 bytes)")
	// Not strictly proof of a bad password — ChangePassword's pre-save re-read
	// collapses a disabled/deleted/concurrently-reset row into this sentinel
	// too — but by far the likeliest cause, so the copy states it without
	// claiming certainty about anything else.
	case errors.Is(err, auth.ErrInvalidCredentials):
		return badRequest("current password is incorrect")
	case err != nil:
		return err
	}
	setSessionCookie(w, r, token, int(a.cfg.Auth.SessionTTL()/time.Second))
	// The identity is the one the middleware resolved before the rotation —
	// still accurate, since a password change moves nothing this response
	// carries (id/email/name/origin/grants). The SPA re-renders its account
	// state from it without a follow-up /auth/me.
	writeJSON(w, http.StatusOK, meFrom(*id))
	return nil
}

func meFrom(id auth.Identity) meResponse {
	var resp meResponse
	resp.User.ID = id.UserID
	resp.User.Email = id.Email
	resp.User.Name = id.Name
	resp.User.Origin = id.Origin
	resp.User.Anonymous = id.Anonymous
	resp.Grants = id.Grants
	if resp.Grants == nil {
		resp.Grants = []auth.Grant{} // wire shape: [] not null
	}
	return resp
}
