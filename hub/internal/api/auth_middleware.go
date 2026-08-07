package api

import (
	"context"
	"errors"
	"log/slog"
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

// guarded is an http.Handler that remembers which authorization it enforces.
// The permissions matrix (GET /api/v1/auth/roles) is built from these as the
// routes register, so it reports the guards the server actually applies
// instead of a hand-written copy that goes stale the first time one changes.
// A plain handler — anything wrapped with bare handle() — carries no guard and
// is reported as public.
type guarded struct {
	http.Handler
	minRole   auth.Role // the role floor; empty when there is none
	adminOnly bool      // global admin ("*" admin grant)
}

// secured wraps a handler with authentication + CSRF. min is the role the
// identity must hold on AT LEAST ONE scope to enter at all; per-project
// enforcement happens in project(). adminOnly routes use securedAdmin.
func (a *API) secured(min auth.Role, fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return guarded{minRole: min, Handler: handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil { // auth disabled — pre-auth behavior
			return fn(w, r)
		}
		if err := a.checkOrigin(r); err != nil {
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
	})}
}

// authenticated requires a valid identity but NO role floor — for routes a
// zero-grant user must still reach (their own logout and /auth/me; a user
// whose grants were all revoked can otherwise neither sign out nor see why).
func (a *API) authenticated(fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return guarded{Handler: handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil { // auth disabled — pre-auth behavior
			return fn(w, r)
		}
		if err := a.checkOrigin(r); err != nil {
			return err
		}
		id, err := a.requestIdentity(w, r)
		if err != nil {
			return err
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		return fn(w, r.WithContext(ctx))
	})}
}

// securedAdmin requires a global admin ("*" admin grant).
func (a *API) securedAdmin(fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return guarded{adminOnly: true, minRole: auth.RoleAdmin, Handler: handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil {
			return fn(w, r)
		}
		if err := a.checkOrigin(r); err != nil {
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
	})}
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

// Modes for Config.OriginCheck, sharing the vocabulary of the chart's other
// mode knob (auth.ingest.mode). OriginCheckEnforce is also the zero value's
// meaning, so a Config that never sets the field keeps the strict behavior.
const (
	OriginCheckEnforce = "enforce"
	OriginCheckLog     = "log"
	OriginCheckOff     = "off"
)

// checkOrigin rejects state-changing cross-origin requests. Non-browser
// clients (no Origin header) pass — the session cookie is the credential.
//
// The Origin is compared to the request's own Host, which is the right
// comparison right up until a reverse proxy rewrites Host (handing the hub the
// ingress address instead of the public domain): the browser then sends the
// correct Origin and the hub sees a Host that will never match it, so every
// write — login included — 403s. Two opt-in ways out, neither of them the
// default: name the real origin in TrustedOrigins (CSRF protection intact), or
// lower OriginCheck.
func (a *API) checkOrigin(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	if a.cfg.OriginCheck == OriginCheckOff {
		return nil
	}
	// An unparseable Origin never matches anything and falls through to the
	// mode decision below — it does not get a free pass.
	if u, err := url.Parse(origin); err == nil && strings.EqualFold(u.Host, r.Host) {
		return nil
	}
	if a.originTrusted(origin) {
		return nil
	}
	if a.cfg.OriginCheck == OriginCheckLog {
		slog.Warn("cross-origin write allowed by auth.originCheck=log — add this origin to auth.trustedOrigins, then restore enforce",
			"origin", origin, "host", r.Host, "path", r.URL.Path)
		return nil
	}
	return forbidden("cross-origin request rejected")
}

// originTrusted reports whether origin is one of the configured trusted
// origins, comparing normalized forms so a trailing slash or the host's casing
// in a values file doesn't quietly stop matching.
func (a *API) originTrusted(origin string) bool {
	want := normalizeOrigin(origin)
	if want == "" {
		return false
	}
	for _, t := range a.cfg.TrustedOrigins {
		if normalizeOrigin(t) == want {
			return true
		}
	}
	return false
}

// normalizeOrigin reduces an origin to lowercase scheme://host, or "" when it
// is not a usable origin (no scheme, no host, unparseable).
func normalizeOrigin(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
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

// oidcCookiePath scopes the transient OIDC flow cookies (state/nonce/verifier)
// to the callback subtree so they are presented only on the OIDC endpoints and
// nowhere else in the app.
const oidcCookiePath = "/api/v1/auth/oidc"

// setShortCookie sets a short-lived (5-minute) HttpOnly cookie carrying OIDC
// flow state (CSRF state, nonce, PKCE verifier) between /oidc/start and the
// callback. Scoped to oidcCookiePath; Secure follows the effective scheme.
func setShortCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: oidcCookiePath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: isTLS(r), MaxAge: 300,
	})
}

// clearShortCookie expires an OIDC flow cookie (same name/path as setShortCookie).
func clearShortCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: oidcCookiePath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: isTLS(r), MaxAge: -1,
	})
}

// cookieVal returns a cookie's value, or "" for a nil cookie (missing).
func cookieVal(c *http.Cookie) string {
	if c == nil {
		return ""
	}
	return c.Value
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
