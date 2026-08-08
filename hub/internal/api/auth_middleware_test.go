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

// authedMux returns a router with auth ON, one seeded editor-on-payments user,
// and that user's session cookie.
func authedMux(t *testing.T) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	return authedMuxWith(t, Config{})
}

// authedMuxWith is authedMux with caller-supplied Config fields (the origin
// policy, here); the auth service is always installed on top of them.
func authedMuxWith(t *testing.T, cfg Config) (*http.ServeMux, *http.Cookie) {
	t.Helper()
	f := &storagetest.Fake{Tenants: []string{"payments", "prod"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(context.Background(), storage.AuthUser{
		ID: "u1", Email: "e@x.io", Name: "E", PasswordHash: h, Origin: "local"})
	_ = f.ReplaceAuthGrants(context.Background(), "u1",
		[]storage.AuthGrant{{UserID: "u1", Scope: "payments", Role: "editor"}})
	token, _, err := svc.Login(context.Background(), "e@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth = svc
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, cfg)
	return mux, &http.Cookie{Name: sessionCookieName, Value: token}
}

// authDo issues a request against mux, optionally attaching a session cookie
// and extra headers. Named distinctly from alerts_test.go's do() (different
// signature) to avoid a redeclaration in this package's test binary.
func authDo(mux *http.ServeMux, method, path string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestNoSessionIs401(t *testing.T) {
	mux, _ := authedMux(t)
	if w := authDo(mux, "GET", "/api/v1/services", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d, want 401", w.Code)
	}
}

// Every route that reads a project's telemetry has to sit behind the session
// middleware. The green routes shipped registered with the bare `handle`
// wrapper, which runs no authentication at all — and because project() treats
// "no identity in context" as "auth is disabled", they served any tenant's
// energy and carbon figures to an unauthenticated caller. Enumerating them
// here, rather than testing one, is the point: the gap was invisible precisely
// because nothing asserted over the set.
func TestEveryProjectDataRouteRequiresASession(t *testing.T) {
	mux, _ := authedMux(t)
	paths := []string{
		"/api/v1/services",
		"/api/v1/service-map",
		"/api/v1/traces",
		"/api/v1/logs",
		"/api/v1/metrics/red",
		"/api/v1/health/groups",
		"/api/v1/alerts",
		"/api/v1/errors/issues",
		"/api/v1/infra/nodes",
		"/api/v1/green/summary",
		"/api/v1/green/budgets",
		"/api/v1/green/report",
	}
	for _, p := range paths {
		if w := authDo(mux, "GET", p, nil, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", p, w.Code)
		}
	}
}

func TestHealthzStaysOpen(t *testing.T) {
	mux, _ := authedMux(t)
	if w := authDo(mux, "GET", "/healthz", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("/healthz: %d", w.Code)
	}
}

func TestGrantedProjectPasses(t *testing.T) {
	mux, c := authedMux(t)
	w := authDo(mux, "GET", "/api/v1/services", c, map[string]string{"X-Avuru-Tenant": "payments"})
	if w.Code != http.StatusOK {
		t.Fatalf("granted project: %d body %s", w.Code, w.Body.String())
	}
}

func TestUngrantedProjectIs403(t *testing.T) {
	mux, c := authedMux(t)
	w := authDo(mux, "GET", "/api/v1/services", c, map[string]string{"X-Avuru-Tenant": "prod"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("ungranted project: %d, want 403", w.Code)
	}
	// Default project resolves to "default", which the user has no grant on.
	if w := authDo(mux, "GET", "/api/v1/services", c, nil); w.Code != http.StatusForbidden {
		t.Fatalf("default project without grant: %d, want 403", w.Code)
	}
}

func TestAdminRouteNeedsGlobalAdmin(t *testing.T) {
	mux, c := authedMux(t)
	w := authDo(mux, "POST", "/api/v1/alerts/channels", c,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("editor on channels create: %d, want 403", w.Code)
	}
}

func TestCrossOriginWriteIs403(t *testing.T) {
	// Uses the error-triage route (registered in this task) — /auth/logout
	// only exists from Task 6 on and would 404 here.
	mux, c := authedMux(t)
	w := authDo(mux, "POST", "/api/v1/errors/issues/123/status", c,
		map[string]string{"Origin": "https://evil.example", "X-Avuru-Tenant": "payments"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d, want 403", w.Code)
	}
}

// TestSameOriginWritePasses proves checkOrigin only blocks CROSS-origin
// writes: an Origin header that matches the request's own Host must sail
// through. The zero-value fake has no seeded error-issue data, so this
// doesn't assert a clean 200 end-to-end — only that the response isn't the
// 403 checkOrigin would produce for a mismatched Origin.
func TestSameOriginWritePasses(t *testing.T) {
	mux, c := authedMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/errors/issues/123/status", strings.NewReader(`{"status":"resolved"}`))
	req.AddCookie(c)
	req.Header.Set("X-Avuru-Tenant", "payments")
	req.Header.Set("Origin", "http://"+req.Host)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("same-origin write rejected as cross-origin: %d body %s", w.Code, w.Body.String())
	}
}

// The four tests below cover the reverse-proxy case: a proxy that rewrites the
// Host header (the ingress address instead of the public domain) makes the
// browser's perfectly correct Origin differ from the Host the hub sees, which
// the strict check reads as cross-origin — 403 on every write, login included.
// TestCrossOriginWriteIs403 above is the pair to these: with neither knob set,
// the strict behavior stands.

func TestTrustedOriginWritePasses(t *testing.T) {
	mux, c := authedMuxWith(t, Config{TrustedOrigins: []string{"https://demo.avuruobs.io"}})
	w := authDo(mux, "POST", "/api/v1/errors/issues/123/status", c,
		map[string]string{"Origin": "https://demo.avuruobs.io", "X-Avuru-Tenant": "payments"})
	if w.Code == http.StatusForbidden {
		t.Fatalf("trusted origin rejected: %d body %s", w.Code, w.Body.String())
	}
}

// A trailing slash and the host's casing are configuration noise, not a
// different origin — normalizing both sides is what keeps a values file from
// failing silently.
func TestTrustedOriginIsNormalized(t *testing.T) {
	mux, c := authedMuxWith(t, Config{TrustedOrigins: []string{"https://Demo.AvuruObs.io/"}})
	w := authDo(mux, "POST", "/api/v1/errors/issues/123/status", c,
		map[string]string{"Origin": "https://demo.avuruobs.io", "X-Avuru-Tenant": "payments"})
	if w.Code == http.StatusForbidden {
		t.Fatalf("trusted origin rejected on casing/slash: %d body %s", w.Code, w.Body.String())
	}
}

// Declaring an allowlist widens it to the listed origins and to nothing else.
func TestUntrustedOriginStillIs403(t *testing.T) {
	mux, c := authedMuxWith(t, Config{TrustedOrigins: []string{"https://demo.avuruobs.io"}})
	w := authDo(mux, "POST", "/api/v1/errors/issues/123/status", c,
		map[string]string{"Origin": "https://evil.example", "X-Avuru-Tenant": "payments"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin with an allowlist set: %d, want 403", w.Code)
	}
}

func TestOriginCheckOffAndLogAllowCrossOrigin(t *testing.T) {
	for _, mode := range []string{"off", "log"} {
		t.Run(mode, func(t *testing.T) {
			mux, c := authedMuxWith(t, Config{OriginCheck: mode})
			w := authDo(mux, "POST", "/api/v1/errors/issues/123/status", c,
				map[string]string{"Origin": "https://demo.avuruobs.io", "X-Avuru-Tenant": "payments"})
			if w.Code == http.StatusForbidden {
				t.Fatalf("originCheck=%s still rejected: %d body %s", mode, w.Code, w.Body.String())
			}
		})
	}
}

func TestAnonymousIdentityScopedAccess(t *testing.T) {
	f := &storagetest.Fake{Tenants: []string{"demo", "prod"}}
	anon := &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "demo", Role: auth.RoleViewer}}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc, AnonymousIdentity: anon})
	if w := authDo(mux, "GET", "/api/v1/services", nil, map[string]string{"X-Avuru-Tenant": "demo"}); w.Code != http.StatusOK {
		t.Fatalf("anonymous on demo: %d", w.Code)
	}
	if w := authDo(mux, "GET", "/api/v1/services", nil, map[string]string{"X-Avuru-Tenant": "prod"}); w.Code != http.StatusForbidden {
		t.Fatalf("anonymous on prod: %d, want 403", w.Code)
	}
}

// TestGarbageCookie proves a cookie naming no known session degrades exactly
// like "no cookie": 401 (with the stale cookie cleared) when there's no
// anonymous identity to fall back to, 200 with the anonymous scope when
// there is. It must NOT read as a store outage (503).
func TestGarbageCookie(t *testing.T) {
	bad := &http.Cookie{Name: sessionCookieName, Value: "garbage"}

	mux, _ := authedMux(t)
	w := authDo(mux, "GET", "/api/v1/services", bad, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("garbage cookie: %d, want 401", w.Code)
	}
	if got := w.Header().Get("Set-Cookie"); got == "" || !strings.Contains(got, sessionCookieName+"=") {
		t.Fatalf("garbage cookie should be cleared via Set-Cookie, got %q", got)
	}

	f := &storagetest.Fake{Tenants: []string{"demo"}}
	anon := &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "demo", Role: auth.RoleViewer}}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	anonMux := http.NewServeMux()
	Register(anonMux, func() storage.Store { return f }, Config{Auth: svc, AnonymousIdentity: anon})
	w2 := authDo(anonMux, "GET", "/api/v1/services", bad, map[string]string{"X-Avuru-Tenant": "demo"})
	if w2.Code != http.StatusOK {
		t.Fatalf("garbage cookie with anon configured: %d, want 200 body %s", w2.Code, w2.Body.String())
	}
}

// TestProjectsFilteredToGrants proves GET /api/v1/projects never leaks a
// project the caller has no grant on: a user granted only "payments" (on a
// store observing both "payments" and "prod") sees exactly ["payments"].
func TestProjectsFilteredToGrants(t *testing.T) {
	mux, c := authedMux(t)
	w := authDo(mux, "GET", "/api/v1/projects", c, map[string]string{"X-Avuru-Tenant": "payments"})
	if w.Code != http.StatusOK {
		t.Fatalf("projects: %d body %s", w.Code, w.Body.String())
	}
	var resp projectsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 1 || resp.Projects[0].ID != "payments" {
		t.Fatalf("projects = %+v, want exactly [payments]", resp.Projects)
	}
}

// TestProjectsIncludesGrantedUnobserved covers the append branch of
// filterProjectsForIdentity: a scope granted to the user but absent from the
// merged config+observed list must still appear, with Source "granted", so
// the switcher can select a project that has no data yet.
func TestProjectsIncludesGrantedUnobserved(t *testing.T) {
	f := &storagetest.Fake{Tenants: []string{"payments"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(context.Background(), storage.AuthUser{
		ID: "u1", Email: "e@x.io", Name: "E", PasswordHash: h, Origin: "local"})
	_ = f.ReplaceAuthGrants(context.Background(), "u1", []storage.AuthGrant{
		{UserID: "u1", Scope: "payments", Role: "editor"},
		{UserID: "u1", Scope: "greenfield", Role: "viewer"}, // no data, no config
	})
	token, _, err := svc.Login(context.Background(), "e@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc})
	c := &http.Cookie{Name: sessionCookieName, Value: token}

	w := authDo(mux, "GET", "/api/v1/projects", c, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("projects: %d body %s", w.Code, w.Body.String())
	}
	var resp projectsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("projects = %+v, want [greenfield payments]", resp.Projects)
	}
	if resp.Projects[0].ID != "greenfield" || resp.Projects[0].Source != "granted" {
		t.Fatalf("projects[0] = %+v, want {greenfield granted}", resp.Projects[0])
	}
	if resp.Projects[1].ID != "payments" || resp.Projects[1].Source != "data" {
		t.Fatalf("projects[1] = %+v, want {payments data}", resp.Projects[1])
	}
}

func TestAuthDisabledKeepsOldBehavior(t *testing.T) {
	f := &storagetest.Fake{}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{}) // Auth nil
	if w := authDo(mux, "GET", "/api/v1/services", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("auth disabled: %d, want 200", w.Code)
	}
}
