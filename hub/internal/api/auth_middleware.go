package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const sessionCookieName = "avuru_session"

type identityKey struct{}

// identityFrom returns the request identity, or nil when auth is disabled.
func identityFrom(ctx context.Context) *auth.Identity {
	id, _ := ctx.Value(identityKey{}).(*auth.Identity)
	return id
}

// secured wraps a handler with authentication + CSRF. min is the role the
// identity must hold on AT LEAST ONE scope to enter at all; per-project
// enforcement happens in project(). adminOnly routes use securedAdmin.
func (a *API) secured(min auth.Role, fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil { // auth disabled — pre-auth behavior
			return fn(w, r)
		}
		if err := checkOrigin(r); err != nil {
			return err
		}
		id, err := a.requestIdentity(w, r)
		if err != nil {
			return err
		}
		if !holdsAnywhere(*id, min) {
			return forbidden("requires %s role", min)
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		return fn(w, r.WithContext(ctx))
	})
}

// securedAdmin requires a global admin ("*" admin grant).
func (a *API) securedAdmin(fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil {
			return fn(w, r)
		}
		if err := checkOrigin(r); err != nil {
			return err
		}
		id, err := a.requestIdentity(w, r)
		if err != nil {
			return err
		}
		if !id.IsAdmin() {
			return forbidden("requires global admin")
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		return fn(w, r.WithContext(ctx))
	})
}

// requestIdentity resolves the session cookie, falling back to the anonymous
// identity when configured. No cookie and no anonymous → 401.
func (a *API) requestIdentity(w http.ResponseWriter, r *http.Request) (*auth.Identity, error) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		id, err := a.cfg.Auth.IdentityFromToken(r.Context(), c.Value)
		if err == nil {
			return &id, nil
		}
		// A store blip must read as 503, not a mass logout or a silent
		// admin→anonymous downgrade: only a genuinely invalid session
		// (unknown/expired/revoked token, or a disabled user — all
		// surfaced by IdentityFromToken as storage.ErrNotFound) falls
		// through to anonymous/401. Any other error (auth.ErrStoreUnavailable,
		// a wrapped ClickHouse failure) means the backend is unreachable, not
		// that the session is bad.
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, errStoreUnavailable
		}
		// The cookie names a session that no longer exists — stop the
		// browser from presenting a dead cookie on every subsequent request.
		clearSessionCookie(w, r)
	}
	if anon := a.cfg.AnonymousIdentity; anon != nil {
		cp := *anon
		return &cp, nil
	}
	return nil, unauthorized()
}

// checkOrigin rejects state-changing cross-origin requests. Non-browser
// clients (no Origin header) pass — the session cookie is the credential.
func checkOrigin(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, r.Host) {
		return forbidden("cross-origin request rejected")
	}
	return nil
}

func holdsAnywhere(id auth.Identity, min auth.Role) bool {
	for _, g := range id.Grants {
		if g.Role.Allows(min) {
			return true
		}
	}
	return false
}

// setSessionCookie/clearSessionCookie manage the login cookie. Secure follows
// the effective scheme (direct TLS or X-Forwarded-Proto from the ingress).
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: isTLS(r), MaxAge: maxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	setSessionCookie(w, r, "", -1)
}

func isTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// project resolves the request's project (X-Avuru-Tenant, default "default")
// and AUTHORIZES it at min role. With auth disabled it degrades to the old
// header-trusting behavior.
func (a *API) project(r *http.Request, min auth.Role) (string, error) {
	t := r.Header.Get("X-Avuru-Tenant")
	if t == "" {
		t = storage.DefaultTenant
	}
	id := identityFrom(r.Context())
	if id == nil {
		return t, nil
	}
	if !id.CanAccess(t, min) {
		return "", forbidden("no %s access to project %q", min, t)
	}
	return t, nil
}
