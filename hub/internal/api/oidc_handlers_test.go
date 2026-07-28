package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"

	"github.com/oauth2-proxy/mockoidc"
)

// oidcMux wires a router with auth ON and the given OIDC accessors. provider or
// settings may be nil to exercise the unconfigured paths; the returned func
// values are still installed (matching how cmd/hub always passes accessors).
func oidcMux(t *testing.T, provider *auth.OIDCProvider, settings *auth.OIDCConfig) *http.ServeMux {
	t.Helper()
	f := &storagetest.Fake{Tenants: []string{"payments"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth:         svc,
		OIDC:         func() *auth.OIDCProvider { return provider },
		OIDCSettings: func() *auth.OIDCConfig { return settings },
	})
	return mux
}

// runMockProvider starts an in-process mock IdP and returns a discovered
// provider bound to it, plus the mock (for QueueUser) and a cleanup.
func runMockProvider(t *testing.T) (*auth.OIDCProvider, *mockoidc.MockOIDC) {
	t.Helper()
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc.Run: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown() })
	cfg := &auth.OIDCConfig{Issuer: m.Issuer(), ClientID: m.ClientID, ClientSecret: m.ClientSecret, GroupsClaim: "groups"}
	p, err := auth.NewOIDCProvider(context.Background(), cfg, "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	return p, m
}

// cookieByName returns the named Set-Cookie from a response, or nil.
func cookieByName(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestOIDCStartNotConfigured proves /oidc/start answers 400 when no provider is
// wired — both when the accessor is unset and when it returns nil.
func TestOIDCStartNotConfigured(t *testing.T) {
	// Accessor returns nil (OIDC configured off / discovery not yet succeeded).
	mux := oidcMux(t, nil, nil)
	w := authDo(mux, "GET", "/api/v1/auth/oidc/start", nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("start (nil provider): %d, want 400", w.Code)
	}

	// Accessor entirely unset on the Config (cfg.OIDC == nil).
	f := &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	bare := http.NewServeMux()
	Register(bare, func() storage.Store { return f }, Config{Auth: svc})
	if w := authDo(bare, "GET", "/api/v1/auth/oidc/start", nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("start (no accessor): %d, want 400", w.Code)
	}
}

// TestOIDCStartRedirectsAndSetsCookies proves a wired provider yields a 302 to
// the IdP with a state= parameter and the three short-lived flow cookies.
func TestOIDCStartRedirectsAndSetsCookies(t *testing.T) {
	p, _ := runMockProvider(t)
	mux := oidcMux(t, p, nil)

	req := httptest.NewRequest("GET", "/api/v1/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("start: %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	if u.Query().Get("state") == "" {
		t.Fatalf("authorize URL missing state=: %q", loc)
	}
	resp := w.Result()
	for _, name := range []string{oidcStateCookie, oidcNonceCookie, oidcVerifierCookie} {
		c := cookieByName(resp, name)
		if c == nil || c.Value == "" {
			t.Fatalf("missing/empty cookie %q: %+v", name, resp.Cookies())
		}
		if !c.HttpOnly || c.Path != oidcCookiePath || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("cookie %q attrs: %+v", name, c)
		}
	}
	// The state cookie must equal the state carried to the IdP.
	if c := cookieByName(resp, oidcStateCookie); c.Value != u.Query().Get("state") {
		t.Fatalf("state cookie %q != authorize state %q", c.Value, u.Query().Get("state"))
	}
}

// TestOIDCCallbackStateMismatchIs403 proves the callback fails closed when the
// state cookie does not match the ?state= query parameter (CSRF guard), before
// any code exchange.
func TestOIDCCallbackStateMismatchIs403(t *testing.T) {
	p, _ := runMockProvider(t)
	mux := oidcMux(t, p, nil)

	req := httptest.NewRequest("GET", "/api/v1/auth/oidc/callback?state=bbb&code=irrelevant", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "aaa"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("state mismatch: %d, want 403", w.Code)
	}
}

// TestOIDCCallbackMissingStateCookieIs403 proves a callback with no state cookie
// at all (e.g. cookies expired or a forged link) is rejected.
func TestOIDCCallbackMissingStateCookieIs403(t *testing.T) {
	p, _ := runMockProvider(t)
	mux := oidcMux(t, p, nil)

	w := authDo(mux, "GET", "/api/v1/auth/oidc/callback?state=x&code=y", nil, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing state cookie: %d, want 403", w.Code)
	}
}

// TestOIDCCallbackNotConfigured proves the callback answers 400 when no provider
// is wired.
func TestOIDCCallbackNotConfigured(t *testing.T) {
	mux := oidcMux(t, nil, nil)
	w := authDo(mux, "GET", "/api/v1/auth/oidc/callback?state=x&code=y", nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("callback (nil provider): %d, want 400", w.Code)
	}
}

// TestOIDCCallbackHappyPath drives the full flow end-to-end: /oidc/start →
// follow the mock IdP's auto-consent redirect to capture code+state → callback
// with the flow cookies → a session cookie is set and the browser is sent to /.
func TestOIDCCallbackHappyPath(t *testing.T) {
	p, m := runMockProvider(t)
	m.QueueUser(&mockoidc.MockUser{Subject: "kc|1", Email: "a@x.io", PreferredUsername: "A", Groups: []string{"obs-admins"}})
	mux := oidcMux(t, p, nil)

	// Start: capture the authorize URL and the flow cookies.
	startReq := httptest.NewRequest("GET", "/api/v1/auth/oidc/start", nil)
	startW := httptest.NewRecorder()
	mux.ServeHTTP(startW, startReq)
	if startW.Code != http.StatusFound {
		t.Fatalf("start: %d, want 302", startW.Code)
	}
	flowCookies := startW.Result().Cookies()
	authorizeURL := startW.Header().Get("Location")

	// Follow the authorize URL against the mock IdP (auto-consents a queued
	// user) and capture ?code=&state= from its redirect back.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	idpResp, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("GET authorize URL: %v", err)
	}
	defer func() { _ = idpResp.Body.Close() }()
	back, err := url.Parse(idpResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse IdP redirect: %v", err)
	}
	code, state := back.Query().Get("code"), back.Query().Get("state")
	if code == "" || state == "" {
		t.Fatalf("IdP redirect missing code/state: %q", idpResp.Header.Get("Location"))
	}

	// Callback: same flow cookies + the returned code/state.
	cbReq := httptest.NewRequest("GET", "/api/v1/auth/oidc/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	for _, c := range flowCookies {
		cbReq.AddCookie(c)
	}
	cbW := httptest.NewRecorder()
	mux.ServeHTTP(cbW, cbReq)

	if cbW.Code != http.StatusFound {
		t.Fatalf("callback: %d body %s, want 302", cbW.Code, cbW.Body.String())
	}
	if loc := cbW.Header().Get("Location"); loc != "/" {
		t.Fatalf("callback Location: %q, want /", loc)
	}
	if got := cbW.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("callback Cache-Control: %q, want no-store", got)
	}
	// A session cookie must be minted; the flow cookies must be cleared.
	resp := cbW.Result()
	sess := cookieByName(resp, sessionCookieName)
	if sess == nil || sess.Value == "" || sess.MaxAge <= 0 {
		t.Fatalf("session cookie not set: %+v", resp.Cookies())
	}
	for _, name := range []string{oidcStateCookie, oidcNonceCookie, oidcVerifierCookie} {
		if c := cookieByName(resp, name); c == nil || c.MaxAge >= 0 {
			t.Fatalf("flow cookie %q not cleared: %+v", name, c)
		}
	}
}

// TestAuthConfigIncludesOIDC proves /auth/config reports "oidc" among methods
// and mirrors ForceSSO once a provider is wired.
func TestAuthConfigIncludesOIDC(t *testing.T) {
	p, _ := runMockProvider(t)
	mux := oidcMux(t, p, &auth.OIDCConfig{ForceSSO: true})

	w := authDo(mux, "GET", "/api/v1/auth/config", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("auth config: %d", w.Code)
	}
	var cfg struct {
		Enabled  bool     `json:"enabled"`
		Methods  []string `json:"methods"`
		ForceSSO bool     `json:"forceSSO"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || len(cfg.Methods) != 2 || cfg.Methods[0] != "local" || cfg.Methods[1] != "oidc" {
		t.Fatalf("methods = %+v, want [local oidc]", cfg.Methods)
	}
	if !cfg.ForceSSO {
		t.Fatalf("forceSSO = false, want true")
	}
}

// TestAuthConfigOIDCOffOmitsMethod proves that with the accessor wired but
// returning nil (discovery not succeeded), only ["local"] is reported and
// ForceSSO stays false even if a settings value happens to be present.
func TestAuthConfigOIDCOffOmitsMethod(t *testing.T) {
	mux := oidcMux(t, nil, &auth.OIDCConfig{ForceSSO: true})
	w := authDo(mux, "GET", "/api/v1/auth/config", nil, nil)
	var cfg struct {
		Methods  []string `json:"methods"`
		ForceSSO bool     `json:"forceSSO"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Methods) != 1 || cfg.Methods[0] != "local" || cfg.ForceSSO {
		t.Fatalf("config with provider nil = %+v, want methods=[local] forceSSO=false", cfg)
	}
}
