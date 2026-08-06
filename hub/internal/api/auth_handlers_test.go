package api

import (
	"context"
	"encoding/json"
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
