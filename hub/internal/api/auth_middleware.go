package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
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

// requestIdentity resolves a credential to an identity: an Authorization
// bearer token first, then the session cookie, then the anonymous identity
// when configured. Nothing left → 401.
//
// Bearer is checked FIRST, and does not fall through. An explicit credential
// beats an ambient one, and an explicit credential that FAILS is an error, not
// something to shrug off: falling through would hand a caller with a bad token
// the anonymous identity on any install that configures one, quietly turning a
// broken CI job into one that reads whatever anonymous can see. A clean 401 is
// the only honest answer. (See design/2026-08-13-api-tokens.md, "The seam".)
func (a *API) requestIdentity(w http.ResponseWriter, r *http.Request) (*auth.Identity, error) {
	if raw, ok := bearerToken(r); ok {
		// An OAuth access token is REFUSED here, on every route that uses this
		// function — which is every route in the router except POST /mcp.
		//
		// Inverted on purpose. The rule is "an MCP credential works against the
		// MCP resource and nothing else", and stating it as a refusal in the
		// shared path means a route added next year is covered by default,
		// whereas an allow-list would have to be remembered. The one function
		// that accepts these tokens is requestIdentityForResource below, and it
		// checks the audience before it returns.
		if strings.HasPrefix(raw, auth.OAuthAccessPrefix) {
			return nil, unauthorized()
		}
		id, err := a.cfg.Auth.IdentityFromAPIToken(r.Context(), raw)
		if err == nil {
			a.touchAPIToken(r.Context(), raw)
			return &id, nil
		}
		// Same distinction the session branch draws below, for the same
		// reason: only ErrNotFound (unknown, revoked, expired, or a disabled
		// owner) means the credential is bad. Anything else means the backend
		// is unreachable, and answering 401 would tell every automated caller
		// to go re-authenticate against a hub that is merely ill.
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, errStoreUnavailable
		}
		return nil, unauthorized()
	}
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

// bearerToken extracts an Authorization bearer credential. The scheme is
// matched case-insensitively (RFC 7235 says it is case-insensitive, and real
// clients do send "bearer").
//
// A header carrying some OTHER scheme, or "Bearer" with nothing after it, is
// reported as absent rather than as a failure: we only claim to understand a
// bearer credential, so anything else falls through to the cookie path instead
// of 401-ing a browser that happens to send an Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	const scheme = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(scheme):])
	return tok, tok != ""
}

// touchAPIToken records that a token was just used, debounced to at most once
// per auth.TouchWindow per token.
//
// It never fails the request. A token that authenticated successfully has
// already earned its access; a full ClickHouse disk must not lock every token
// client out over a bookkeeping column.
func (a *API) touchAPIToken(ctx context.Context, raw string) {
	hash := auth.HashAPIToken(raw)
	if !a.lastUsed.ShouldTouch(hash, time.Now()) {
		return
	}
	st, err := a.store()
	if err != nil {
		return
	}
	if err := st.TouchAuthToken(ctx, hash, time.Now()); err != nil {
		slog.Debug("recording API token last-use failed", "error", err)
	}
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
//
// Reads should call projectTenants instead — a read on an aggregate must span
// its members. What legitimately stays here is anything that writes or is
// otherwise single-tenant by nature: error triage, a test notification, the
// profiles ingest path, and projectTenants' own first step.
func (a *API) project(r *http.Request, min auth.Role) (string, error) {
	t := r.Header.Get("X-Avuru-Tenant")
	if t == "" {
		t = storage.DefaultTenant
	}
	// An OAuth token names the project its owner consented to, and that wins.
	// A hosted MCP client cannot set X-Avuru-Tenant, so without this every
	// connector would silently read the default project and be useless on a
	// multi-project install. A header naming a DIFFERENT project is refused
	// rather than quietly overridden: the token says what was consented to, and
	// disagreeing with it is a caller error, not a preference.
	//
	// This narrows within the owner's live grants — the CanAccess check below
	// still runs — so it is not a second authorization system to keep in step.
	if b := oauthBindingFrom(r.Context()); b != nil && b.Project != "" {
		if r.Header.Get("X-Avuru-Tenant") != "" && r.Header.Get("X-Avuru-Tenant") != b.Project {
			return "", forbidden("this credential is scoped to project %q", b.Project)
		}
		t = b.Project
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

// projectTenants is project() plus member expansion: it authorizes the
// request's project at min, then resolves the tenant set its queries must
// read (the project itself for a leaf, its authorized members for an
// aggregate — see resolveTenants).
func (a *API) projectTenants(r *http.Request, min auth.Role) (project string, tenants []string, err error) {
	project, err = a.project(r, min)
	if err != nil {
		return "", nil, err
	}
	tenants, err = a.resolveTenants(r.Context(), project, identityFrom(r.Context()))
	if err != nil {
		return "", nil, err
	}
	return project, tenants, nil
}

// oauthKey carries the OAuth binding through the request context.
//
// Separate from identityKey on purpose: auth.Identity is the enterprise seam
// and is serialized to every client by /auth/me, so an OAuth concept has no
// business on it. Absent for a session or an API token, which is exactly what
// makes "is this an OAuth request" answerable.
type oauthKey struct{}

func oauthBindingFrom(ctx context.Context) *auth.OAuthBinding {
	b, _ := ctx.Value(oauthKey{}).(*auth.OAuthBinding)
	return b
}

// requestIdentityForResource is the ONLY path that accepts an OAuth access
// token, and it accepts one only for the resource it was minted for.
//
// Everything else — secured, authenticated, securedAdmin — goes through
// requestIdentity, which refuses these tokens outright. A route is covered by
// default; opting in is deliberate and visible.
func (a *API) requestIdentityForResource(w http.ResponseWriter, r *http.Request, resource string) (*auth.Identity, *auth.OAuthBinding, error) {
	raw, ok := bearerToken(r)
	if !ok || !strings.HasPrefix(raw, auth.OAuthAccessPrefix) {
		// Not an OAuth credential: fall back to the ordinary rules, so a
		// personal API token and a browser session keep working exactly as
		// they did before OAuth existed.
		id, err := a.requestIdentity(w, r)
		return id, nil, err
	}
	id, binding, err := a.cfg.Auth.IdentityFromOAuthToken(r.Context(), raw)
	if err != nil {
		// Same distinction every other branch draws: only ErrNotFound means
		// the credential is bad. A store blip must read as 503, not as "go
		// re-authenticate".
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, nil, errStoreUnavailable
		}
		return nil, nil, unauthorized()
	}
	// The audience check. A token minted for one resource must not be
	// replayable against another, and the comparison is against the row read
	// this request rather than anything the caller supplied.
	if binding.Resource != resource {
		return nil, nil, unauthorized()
	}
	if !scopeAllows(binding.Scope, scopeMCPRead) {
		return nil, nil, forbidden("this credential does not carry the %s scope", scopeMCPRead)
	}
	a.touchOAuthToken(r.Context(), raw)
	return &id, &binding, nil
}

// scopeMCPRead is the only scope this server issues today. Read-only, and named
// so that a wider one later is a visible addition rather than a silent widening.
const scopeMCPRead = "mcp:read"

// scopeAllows reports whether a space-delimited scope string contains want.
func scopeAllows(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}

// touchOAuthToken records last use through the same debounce API tokens use —
// an access token is presented on every tool call, and a write per call would
// turn a read path into a write path.
func (a *API) touchOAuthToken(ctx context.Context, raw string) {
	hash := auth.HashAPIToken(raw)
	if !a.lastUsed.ShouldTouch(hash, time.Now()) {
		return
	}
	st, err := a.store()
	if err != nil {
		return
	}
	if err := st.TouchOAuthToken(ctx, hash, time.Now()); err != nil {
		slog.Debug("recording OAuth token last-use failed", "error", err)
	}
}

// securedResource is secured() for a route that owns its own OAuth audience.
// Only POST /mcp uses it.
//
// A 401 from here carries the RFC 9728 WWW-Authenticate challenge, which is how
// an MCP client discovers where to authenticate: without it a connector sees a
// bare 401 and has nowhere to go.
func (a *API) securedResource(min auth.Role, resource func(*http.Request) string, fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return guarded{minRole: min, Handler: handle(func(w http.ResponseWriter, r *http.Request) error {
		if a.cfg.Auth == nil {
			return fn(w, r)
		}
		if err := a.checkOrigin(r); err != nil {
			return err
		}
		res := resource(r)
		id, binding, err := a.requestIdentityForResource(w, r, res)
		if err != nil {
			// The RFC 9728 challenge goes on the 401 itself: it is how a client
			// that has never seen this server discovers where to authenticate.
			// Identified by status rather than by sentinel, because
			// unauthorized() builds a fresh error each time.
			var ae *apiError
			if errors.As(err, &ae) && ae.status == http.StatusUnauthorized {
				a.setResourceChallenge(w, res)
			}
			return err
		}
		if !holdsAnywhere(*id, min) {
			return forbidden("requires %s on at least one project", min)
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		if binding != nil {
			ctx = context.WithValue(ctx, oauthKey{}, binding)
		}
		return fn(w, r.WithContext(ctx))
	})}
}

// setResourceChallenge writes the RFC 9728 WWW-Authenticate challenge.
//
// This header is the entire discovery bootstrap: a client that has never seen
// this server gets a 401, reads resource_metadata, fetches that document, finds
// the authorization server and starts the flow. Without it a connector sees a
// bare 401 and has nowhere to go — which is why it is set on the 401 rather
// than only advertised somewhere a client would have to already know about.
func (a *API) setResourceChallenge(w http.ResponseWriter, resource string) {
	meta := a.protectedResourceMetadataURL(resource)
	if meta == "" {
		// No public URL configured, so there is no absolute document to point
		// at. A challenge naming an unreachable URL is worse than none.
		w.Header().Set("WWW-Authenticate", `Bearer realm="avuru-obs", error="invalid_token"`)
		return
	}
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer realm="avuru-obs", error="invalid_token", error_description=%q, resource_metadata=%q`,
		"the credential is missing, expired, or not valid for this resource", meta))
}

// protectedResourceMetadataURL is where a client can read what this resource is
// and who guards it. Empty when no public URL is configured: a challenge naming
// a URL nobody can fetch is worse than a bare one.
func (a *API) protectedResourceMetadataURL(resource string) string {
	if a.cfg.PublicURL == "" || resource == "" {
		return ""
	}
	return oauth.Issuer(a.cfg.PublicURL) + oauth.PathWellKnownPRMCP
}

// mcpResource is the audience POST /mcp guards. A single function so the
// metadata document, the token's stored audience and this check are the same
// string by construction.
func (a *API) mcpResource(*http.Request) string {
	return oauth.ResourceURI(a.cfg.PublicURL)
}
