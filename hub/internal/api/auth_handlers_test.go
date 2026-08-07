package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestDemoLogin(t *testing.T) {
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if err := svc.EnsureDemoUser(context.Background(), "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	a := &API{provider: func() storage.Store { return f }, cfg: Config{
		Auth: svc, DemoEnabled: true, DemoEmail: "demo@avuru.obs", DemoPassword: "demo-pw",
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/demo", nil)
	if err := a.handleDemoLogin(rec, req); err != nil {
		t.Fatalf("demo login: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// A session cookie was set and the identity is the read-only demo viewer.
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("no session cookie set")
	}
	var me meResponse
	_ = json.NewDecoder(rec.Body).Decode(&me)
	if len(me.Grants) != 1 || me.Grants[0].Scope != "demo" {
		t.Fatalf("grants = %+v, want viewer@demo", me.Grants)
	}
	// The demo viewer is an ordinary LOCAL account, so origin alone would tell
	// the SPA to render the change-password form — which ChangePassword then
	// refuses by reserved id. passwordChange is what stops the SPA offering a
	// form the hub will 403.
	if me.User.Origin != "local" {
		t.Fatalf("origin = %q, want local (the premise of the next assertion)", me.User.Origin)
	}
	if me.User.PasswordChange != passwordChangeShared {
		t.Fatalf("passwordChange = %q, want %q", me.User.PasswordChange, passwordChangeShared)
	}
}

// passwordChangeFor must agree with Service.ChangePassword about who may
// rotate a password here — the two drifting is precisely how the demo account
// came to be shown a form it could never submit.
func TestPasswordChangeForMatchesTheRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   auth.Identity
		want string
	}{
		{"local user rotates here", auth.Identity{UserID: "u1", Origin: "local"}, passwordChangeSelf},
		// Allow-listed on "local", so an origin this build has never heard of
		// defaults to "not here" rather than to a form the hub would reject.
		{"sso user is owned by the IdP", auth.Identity{UserID: "u2", Origin: "oidc"}, passwordChangeIdP},
		{"unknown origin fails closed", auth.Identity{UserID: "u3", Origin: "ldap"}, passwordChangeIdP},
		{"shared demo account", auth.Identity{UserID: auth.DemoViewerID, Origin: "local"}, passwordChangeShared},
		{"anonymous has no account", auth.Identity{Anonymous: true}, ""},
		// Origin is checked BEFORE the demo id, mirroring ChangePassword, so a
		// non-local demo row would report the IdP reason the hub would give.
		{"origin wins over the demo id", auth.Identity{UserID: auth.DemoViewerID, Origin: "oidc"}, passwordChangeIdP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := passwordChangeFor(tc.id); got != tc.want {
				t.Fatalf("passwordChangeFor(%+v) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestAuthConfigAdvertisesDemo(t *testing.T) {
	a := &API{cfg: Config{Auth: &auth.Service{}, DemoEnabled: true}}
	rec := httptest.NewRecorder()
	_ = a.handleAuthConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))
	if !strings.Contains(rec.Body.String(), `"demoEnabled":true`) {
		t.Fatalf("config missing demoEnabled: %s", rec.Body)
	}
}

func TestLoginSetsCookieAndMeWorks(t *testing.T) {
	mux, _ := authedMux(t)

	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"e@x.io","password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d body %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("cookie: %+v", cookies)
	}
	if cookies[0].MaxAge <= 0 || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attrs: %+v", cookies[0])
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("login Cache-Control: %q, want no-store", got)
	}

	me := authDo(mux, "GET", "/api/v1/auth/me", cookies[0], nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d", me.Code)
	}
	var resp struct {
		User struct {
			Email     string `json:"email"`
			Anonymous bool   `json:"anonymous"`
		} `json:"user"`
		Grants []struct {
			Scope string `json:"scope"`
			Role  string `json:"role"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.User.Email != "e@x.io" || len(resp.Grants) != 1 || resp.Grants[0].Scope != "payments" {
		t.Fatalf("me payload: %+v", resp)
	}
}

func TestLoginBadPasswordIs401(t *testing.T) {
	mux, _ := authedMux(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"e@x.io","password":"nope"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d, want 401", w.Code)
	}
}

func TestLogoutRevokes(t *testing.T) {
	mux, c := authedMux(t)
	if w := authDo(mux, "POST", "/api/v1/auth/logout", c, nil); w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := authDo(mux, "GET", "/api/v1/services", c, map[string]string{"X-Avuru-Tenant": "payments"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d, want 401", w.Code)
	}
}

// TestLoginStoreUnavailableIs503 proves a store outage during login/logout
// reads as 503 (consistent with requestIdentity), not the generic 500
// handle() would otherwise map an unwrapped auth.ErrStoreUnavailable to. The
// logout assertion here exercises the a.authenticated() middleware's 503 path
// (requestIdentity fails resolving the cookie before handleLogout ever runs)
// — handleLogout's own ErrStoreUnavailable wrapping is defense-in-depth for
// the narrower case where identity resolves but the revoke call itself then
// hits a store outage.
func TestLoginStoreUnavailableIs503(t *testing.T) {
	svc := auth.NewService(func() storage.Store { return nil }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return &storagetest.Fake{} }, Config{Auth: svc})

	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"e@x.io","password":"pw"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("login store down: %d, want 503", w.Code)
	}

	c := &http.Cookie{Name: sessionCookieName, Value: "whatever"}
	if w := authDo(mux, "POST", "/api/v1/auth/logout", c, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout store down: %d, want 503", w.Code)
	}
}

// TestAuthConfigReportsMethods covers both states of the ALWAYS-registered
// /auth/config route: auth on reports enabled:true + ["local"]; auth off
// (Config{} — no cfg.Auth) still answers 200, not 404, with enabled:false and
// an empty methods list — the SPA's login page never special-cases a 404.
func TestAuthConfigReportsMethods(t *testing.T) {
	mux, _ := authedMux(t)
	w := authDo(mux, "GET", "/api/v1/auth/config", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("auth config: %d", w.Code)
	}
	var cfg struct {
		Enabled bool     `json:"enabled"`
		Methods []string `json:"methods"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || len(cfg.Methods) != 1 || cfg.Methods[0] != "local" {
		t.Fatalf("auth config (enabled): %+v", cfg)
	}

	disabledMux := http.NewServeMux()
	Register(disabledMux, func() storage.Store { return &storagetest.Fake{} }, Config{})
	w2 := authDo(disabledMux, "GET", "/api/v1/auth/config", nil, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("auth config (disabled): %d", w2.Code)
	}
	var cfg2 struct {
		Enabled bool     `json:"enabled"`
		Methods []string `json:"methods"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Enabled || len(cfg2.Methods) != 0 {
		t.Fatalf("auth config (disabled): %+v", cfg2)
	}
}

// TestLoginCrossOriginIs403 proves handleLogin's own checkOrigin call rejects
// a cross-origin POST (it isn't wrapped by secured()/authenticated(), which
// run checkOrigin for other routes, so this path needs its own coverage).
func TestLoginCrossOriginIs403(t *testing.T) {
	mux, _ := authedMux(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"e@x.io","password":"pw"}`))
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login: %d, want 403", w.Code)
	}
}

// TestLoginBehindProxyWithTrustedOrigin is the reverse-proxy case end to end,
// on the route that hurts most: the browser sends the origin the user typed,
// the proxy has replaced Host with the ingress address (httptest's fixed
// "example.com" stands in for it), and the login still has to succeed —
// because that origin is declared. Without the declaration this is
// TestLoginCrossOriginIs403.
func TestLoginBehindProxyWithTrustedOrigin(t *testing.T) {
	mux, _ := authedMuxWith(t, Config{TrustedOrigins: []string{"https://demo.avuruobs.io"}})
	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"email":"e@x.io","password":"pw"}`))
	req.Header.Set("Origin", "https://demo.avuruobs.io")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login behind a Host-rewriting proxy: %d body %s", w.Code, w.Body.String())
	}
	var signedIn bool
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			signedIn = true
		}
	}
	if !signedIn {
		t.Fatal("login returned 200 without a session cookie")
	}
}

// TestLoginRateLimit429 drives 6 bad logins for the same email from
// httptest's fixed RemoteAddr (same "email|ip" key every time): the 6th
// trips maxLoginAttempts and must answer 429 with Retry-After: 60.
func TestLoginRateLimit429(t *testing.T) {
	mux, _ := authedMux(t)
	var w *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("POST", "/api/v1/auth/login",
			strings.NewReader(`{"email":"e@x.io","password":"wrong"}`))
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th bad login: %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After: %q, want \"60\"", got)
	}
}

func TestLoginGarbageJSONIs400(t *testing.T) {
	mux, _ := authedMux(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("garbage JSON: %d, want 400", w.Code)
	}
}

func TestLoginMissingPasswordIs400(t *testing.T) {
	mux, _ := authedMux(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"e@x.io"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing password: %d, want 400", w.Code)
	}
}

// TestLoginBodyTooLargeIs413 proves the MaxBytesReader cap on handleLogin's
// body actually surfaces as 413 (decodeJSONError must NOT swallow the
// *http.MaxBytesError into a generic 400). The oversized field must be
// syntactically valid JSON so the decoder reads deep enough into the string
// value to hit the reader's byte cap, rather than failing fast on a syntax
// error.
func TestLoginBodyTooLargeIs413(t *testing.T) {
	mux, _ := authedMux(t)
	big := strings.Repeat("a", 70*1024) // > 64KiB cap
	body := `{"email":"e@x.io","password":"` + big + `"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login body: %d, want 413", w.Code)
	}
}

// Self-service password change: wrong current -> 400 (NOT 401 — the SPA
// treats 401 as session-expired and redirects to login), success rotates the
// cookie, /auth/me carries origin so the SPA knows a password form applies.
func TestChangeOwnPassword(t *testing.T) {
	mux, c, _ := adminMux(t)

	me := doBody(mux, "GET", "/api/v1/auth/me", c, "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"origin":"local"`) {
		t.Fatalf("me without origin: %d %s", me.Code, me.Body.String())
	}

	w := doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"WRONG","newPassword":"next-pw"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong current: %d, want 400; body %s", w.Code, w.Body.String())
	}

	w = doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"root-pw","newPassword":"next-pw"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("change: %d body %s", w.Code, w.Body.String())
	}
	// The body is the contract, not just the status: the SPA re-renders its
	// account state from this response instead of following up with
	// /auth/me, so an empty 200 would silently break the Account tab.
	var body meResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("change response body: %v (%s)", err, w.Body.String())
	}
	if body.User.ID == "" || body.User.Origin != "local" || body.User.Anonymous {
		t.Fatalf("change response must carry the caller's identity: %s", w.Body.String())
	}
	var fresh *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookieName && ck.Value != "" {
			fresh = ck
		}
	}
	if fresh == nil {
		t.Fatal("no fresh session cookie on the response")
	}
	if got := doBody(mux, "GET", "/api/v1/auth/me", c, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("old session after rotation: %d, want 401", got.Code)
	}
	if got := doBody(mux, "GET", "/api/v1/auth/me", fresh, ""); got.Code != http.StatusOK {
		t.Fatalf("fresh session: %d", got.Code)
	}
}

func TestChangeOwnPasswordRequiresBody(t *testing.T) {
	mux, c, _ := adminMux(t)
	w := doBody(mux, "POST", "/api/v1/auth/password", c, `{"currentPassword":"","newPassword":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty fields: %d, want 400", w.Code)
	}
	if w := doBody(mux, "POST", "/api/v1/auth/password", nil, `{"currentPassword":"a","newPassword":"b"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d, want 401", w.Code)
	}
}

// In anonymous mode a cookie-less caller still gets an identity from the
// middleware, so "no session" alone would NOT stop here — only the explicit
// Anonymous check does. Without it the synthetic identity's empty UserID
// would reach ChangePassword and surface as a confusing 404.
func TestChangeOwnPasswordRefusedForAnonymous(t *testing.T) {
	f := &storagetest.Fake{Tenants: []string{"demo"}}
	anon := &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "demo", Role: auth.RoleViewer}}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc, AnonymousIdentity: anon})

	w := doBody(mux, "POST", "/api/v1/auth/password", nil, `{"currentPassword":"a","newPassword":"b"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous password change: %d, want 401; body %s", w.Code, w.Body.String())
	}
}

// A new password bcrypt refuses (>72 bytes) is the CALLER's error about the
// NEW password: 400, never the generic 500 the unmapped sentinel would fall
// through to — and never sharing the wrong-current-password message, which
// would send the user hunting for a typo in the field that was fine. This is
// exactly why auth.ErrPasswordUnusable exists as its own sentinel.
func TestChangeOwnPasswordRejectsUnusableNewPassword(t *testing.T) {
	mux, c, _ := adminMux(t)
	longPw := strings.Repeat("a", 73) // bcrypt caps at 72 bytes
	w := doBody(mux, "POST", "/api/v1/auth/password", c,
		`{"currentPassword":"root-pw","newPassword":"`+longPw+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("73-byte new password: %d, want 400; body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "new password") {
		t.Fatalf("400 body doesn't blame the new password: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "current password is incorrect") {
		t.Fatalf("unusable new password reported as a wrong current password: %s", w.Body.String())
	}
	// That nothing was written on this path is TestChangePasswordRejects-
	// UnusableNewPassword's job, at the layer that owns the write.
}

// TestChangeOwnPasswordRateLimit429 mirrors TestLoginRateLimit429 for the
// rotation path: a stolen session must not become a password-guessing oracle,
// so wrong-current attempts lock out at the same ceiling — with Retry-After,
// since the browser has no other way to know how long the wait is.
func TestChangeOwnPasswordRateLimit429(t *testing.T) {
	mux, c, _ := adminMux(t)
	var w *httptest.ResponseRecorder
	// httptest's fixed RemoteAddr keys every attempt to the same limiter
	// bucket; the 6th trips maxLoginAttempts.
	for i := 0; i < 6; i++ {
		w = doBody(mux, "POST", "/api/v1/auth/password", c,
			`{"currentPassword":"WRONG","newPassword":"next-pw"}`)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th wrong current password: %d, want 429; body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After: %q, want \"60\"", got)
	}
}

// The route is registered through authenticated(), which runs checkOrigin —
// so the handler deliberately does not repeat it. This pins that the
// protection is actually there: a cross-origin POST carrying a valid session
// cookie is exactly the CSRF this endpoint must not honour.
func TestChangeOwnPasswordCrossOriginIs403(t *testing.T) {
	mux, c, _ := adminMux(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/password",
		strings.NewReader(`{"currentPassword":"root-pw","newPassword":"next-pw"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin password change: %d, want 403; body %s", w.Code, w.Body.String())
	}
}

// An origin=oidc user's credential lives at the IdP; the change is refused
// with a message that says so (400, not the wrong-password copy). /auth/me
// reports the origin, which is how the SPA knows not to render the form at all.
func TestChangeOwnPasswordRejectedForSSOUser(t *testing.T) {
	mux, _, f := adminMux(t)

	// A sibling *auth.Service over the same fake store mints the SSO session
	// (same pattern as TestPasswordRotationRevokesSessions) — CompleteSSO is
	// the only way an origin=oidc user ever gets one.
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	token, _, err := svc.CompleteSSO(context.Background(), auth.ExternalIdentity{
		Subject: "sub-pw", Email: "sso@x.io", Name: "SSO",
	})
	if err != nil {
		t.Fatalf("complete sso: %v", err)
	}
	c := &http.Cookie{Name: sessionCookieName, Value: token}

	me := doBody(mux, "GET", "/api/v1/auth/me", c, "")
	if !strings.Contains(me.Body.String(), `"origin":"oidc"`) {
		t.Fatalf("me for an SSO user: %s", me.Body.String())
	}

	w := doBody(mux, "POST", "/api/v1/auth/password", c,
		`{"currentPassword":"anything","newPassword":"next-pw"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("sso password change: %d, want 400; body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "identity provider") {
		t.Fatalf("400 body doesn't name the identity provider: %s", w.Body.String())
	}
	// The property that matters: no local credential was minted behind the
	// IdP's back — Login resolves by email without filtering Origin.
	if got := doBody(mux, "POST", "/api/v1/auth/login", nil,
		`{"email":"sso@x.io","password":"next-pw"}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("login with the refused password: %d, want 401; body %s", got.Code, got.Body.String())
	}
}

// The shared demo account is server-managed: EnsureDemoUser re-keys it from
// the configured credentials on every boot, so a visitor "successfully"
// changing it would silently revert — and would lock every other visitor out
// until the next restart. 403, and the demo credentials still work.
func TestChangeOwnPasswordRefusedForDemoAccount(t *testing.T) {
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if err := svc.EnsureDemoUser(context.Background(), "demo@avuru.obs", "demo-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "demo@avuru.obs", "demo-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth: svc, DemoEnabled: true, DemoEmail: "demo@avuru.obs", DemoPassword: "demo-pw",
	})
	c := &http.Cookie{Name: sessionCookieName, Value: token}

	// Refused AND advertised as refused, together in one test: the SPA reads
	// passwordChange to decide whether to render the form at all, so a 403 that
	// /auth/me doesn't warn about is the bug — a visitor fills the form in and
	// collects the refusal only on submit.
	if me := doBody(mux, "GET", "/api/v1/auth/me", c, ""); !strings.Contains(
		me.Body.String(), `"passwordChange":"shared"`) {
		t.Fatalf("me for the demo viewer doesn't advertise the refusal: %s", me.Body.String())
	}

	w := doBody(mux, "POST", "/api/v1/auth/password", c,
		`{"currentPassword":"demo-pw","newPassword":"visitor-pw"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("demo password change: %d, want 403; body %s", w.Code, w.Body.String())
	}
	if got := doBody(mux, "POST", "/api/v1/auth/login", nil,
		`{"email":"demo@avuru.obs","password":"demo-pw"}`); got.Code != http.StatusOK {
		t.Fatalf("demo login after the refused change: %d, want 200 (password untouched)", got.Code)
	}
}

// revokeBreakingStore fails the session sweep once armed, reproducing the
// post-save seam in ChangePassword / handleUpdateUser. It is armed only after
// bootstrap and login are done, so setup still works.
type revokeBreakingStore struct {
	*storagetest.Fake
	armed bool
}

func (s *revokeBreakingStore) RevokeAuthSessionsForUser(ctx context.Context, id string) error {
	if s.armed {
		return errors.New("clickhouse unavailable")
	}
	return s.Fake.RevokeAuthSessionsForUser(ctx, id)
}

// brokenMux is adminMux over a store whose session sweep can be broken.
func brokenMux(t *testing.T) (*http.ServeMux, *http.Cookie, *revokeBreakingStore) {
	t.Helper()
	f := &storagetest.Fake{}
	st := &revokeBreakingStore{Fake: f}
	svc := auth.NewService(func() storage.Store { return st }, time.Hour)
	if _, err := svc.Bootstrap(context.Background(), "root-pw"); err != nil {
		t.Fatal(err)
	}
	token, _, err := svc.Login(context.Background(), "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return st }, Config{Auth: svc})
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}, st
}

// A rotation that saved but could not sweep sessions still answers 5xx — the
// server did fail — but the BODY must say the password changed. The generic
// "internal error" this used to return reads as "nothing happened", which
// sends the user back to a password that no longer works.
func TestChangePasswordHalfAppliedBodyTellsTheTruth(t *testing.T) {
	mux, c, st := brokenMux(t)
	st.armed = true

	w := doBody(mux, "POST", "/api/v1/auth/password", c,
		`{"currentPassword":"root-pw","newPassword":"next-pw"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500; body %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "internal error") {
		t.Fatalf("generic body hides a completed rotation: %s", body)
	}
	if !strings.Contains(body, "your password was changed") {
		t.Fatalf("body must state the password changed: %s", body)
	}

	// And it is true: the sweep failed, so the OLD session is still usable —
	// but the new password is the one that logs in from here.
	st.armed = false
	if got := doBody(mux, "GET", "/api/v1/auth/me", c, ""); got.Code != http.StatusOK {
		t.Fatalf("sweep failed, so the old cookie should still work: %d", got.Code)
	}
	if _, _, err := auth.NewService(func() storage.Store { return st.Fake }, time.Hour).
		Login(context.Background(), "admin", "next-pw", "ip"); err != nil {
		t.Fatalf("the new password is not in effect: %v", err)
	}
}

// Same seam on the admin route: the reset landed, so a bare 500 would have the
// admin believe the account still holds its old credential.
func TestAdminResetHalfAppliedBodyTellsTheTruth(t *testing.T) {
	mux, c, st := brokenMux(t)

	w := doBody(mux, "POST", "/api/v1/users", c,
		`{"email":"dev@x.io","name":"Dev","password":"devpw","grants":[]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d body %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	st.armed = true
	w = doBody(mux, "PUT", "/api/v1/users/"+created.ID, c, `{"password":"reset-pw"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500; body %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "internal error") {
		t.Fatalf("generic body hides a completed reset: %s", body)
	}
	if !strings.Contains(body, "the password was changed") {
		t.Fatalf("body must state the password changed: %s", body)
	}
	if _, _, err := auth.NewService(func() storage.Store { return st.Fake }, time.Hour).
		Login(context.Background(), "dev@x.io", "reset-pw", "ip"); err != nil {
		t.Fatalf("the reset is not in effect: %v", err)
	}
}
