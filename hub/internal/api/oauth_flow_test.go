package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

const (
	testRedirect = "https://app.example.com/cb"
	testVerifier = "0123456789012345678901234567890123456789012345678901234567890123"
)

func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// registerClient does the RFC 7591 half and returns the issued client_id.
func registerClient(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	w := postJSON(mux, oauth.PathRegister,
		`{"client_name":"Test Connector","redirect_uris":["`+testRedirect+`"],"scope":"mcp:read offline_access"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	var got oauth.RegistrationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.ClientID
}

func authorizeQuery(clientID string) url.Values {
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("redirect_uri", testRedirect)
	v.Set("response_type", "code")
	v.Set("code_challenge", testChallenge())
	v.Set("code_challenge_method", "S256")
	v.Set("scope", "mcp:read offline_access")
	v.Set("state", "xyz")
	return v
}

func getWith(mux *http.ServeMux, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func postForm(mux *http.ServeMux, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// The whole flow, end to end: register, authorize, consent, exchange, and then
// actually read the estate with what came out.
func TestOAuthFlowEndToEnd(t *testing.T) {
	mux, _, _, cookie := oauthFlowMux(t)
	clientID := registerClient(t, mux)
	q := authorizeQuery(clientID)

	// Signed in: /authorize sends the person to the consent screen.
	w := getWith(mux, oauth.PathAuthorize+"?"+q.Encode(), cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize = %d, want 302: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/consent?") {
		t.Fatalf("authorize sent us to %q, want the consent page", loc)
	}

	// The consent screen's own data.
	consentQ := strings.TrimPrefix(loc, "/oauth/consent?")
	w = getWith(mux, oauth.PathConsent+"?"+consentQ, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("consent view = %d: %s", w.Code, w.Body.String())
	}
	var view consentView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ClientName != "Test Connector" {
		t.Errorf("client name = %q", view.ClientName)
	}
	// Never verified: the name is whatever the client typed at registration.
	if view.ClientVerified {
		t.Error("a self-declared client name was presented as verified")
	}
	if view.RedirectHost != "app.example.com" {
		t.Errorf("redirect host = %q, want the checkable fact", view.RedirectHost)
	}

	// Approve.
	req := httptest.NewRequest("POST", oauth.PathConsent+"?"+consentQ,
		strings.NewReader(`{"approve":true,"project":"prod"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("consent decide = %d: %s", w.Code, w.Body.String())
	}
	var decided map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &decided)
	redirect, err := url.Parse(decided["redirect"])
	if err != nil {
		t.Fatal(err)
	}
	code := redirect.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %q", decided["redirect"])
	}
	if redirect.Query().Get("state") != "xyz" {
		t.Error("state was not echoed back")
	}

	// Exchange.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", testRedirect)
	form.Set("code_verifier", testVerifier)
	w = postForm(mux, oauth.PathToken, form)
	if w.Code != http.StatusOK {
		t.Fatalf("token = %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on a credential", cc)
	}
	var tok tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("token response = %+v", tok)
	}

	// And the point of all of it: the token reads the estate.
	if r := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + tok.AccessToken}); r.Code != http.StatusOK {
		t.Fatalf("the issued token could not call /mcp: %d %s", r.Code, r.Body.String())
	}
}

// An unauthenticated visitor is sent to sign in and brought back — a 401 is
// something a browser cannot act on.
func TestAuthorizeSendsAnAnonymousVisitorToLogin(t *testing.T) {
	mux, _, _, _ := oauthFlowMux(t)
	clientID := registerClient(t, mux)
	w := getWith(mux, oauth.PathAuthorize+"?"+authorizeQuery(clientID).Encode(), nil)
	if w.Code != http.StatusFound {
		t.Fatalf("anonymous authorize = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("Location = %q, want /login?next=", loc)
	}
	// The round trip must actually come back here, or the person is stranded.
	next, _ := url.QueryUnescape(strings.TrimPrefix(loc, "/login?next="))
	if !strings.HasPrefix(next, oauth.PathAuthorize) {
		t.Errorf("next = %q, want a return to /authorize", next)
	}
}

// THE open-redirect test. An unknown client, or a redirect URI that is not
// registered, must never produce a redirect TO that URI.
func TestAuthorizeNeverRedirectsToAnUnprovenURI(t *testing.T) {
	mux, _, _, cookie := oauthFlowMux(t)
	clientID := registerClient(t, mux)

	cases := map[string]url.Values{}
	unknown := authorizeQuery("no-such-client")
	unknown.Set("redirect_uri", "https://evil.test/steal")
	cases["unknown client"] = unknown

	wrong := authorizeQuery(clientID)
	wrong.Set("redirect_uri", "https://evil.test/steal")
	cases["unregistered redirect"] = wrong

	prefix := authorizeQuery(clientID)
	prefix.Set("redirect_uri", testRedirect+".evil.test")
	cases["prefix-extended redirect"] = prefix

	for name, q := range cases {
		w := getWith(mux, oauth.PathAuthorize+"?"+q.Encode(), cookie)
		if w.Code == http.StatusFound {
			t.Errorf("%s: redirected to %q — must be shown to the person instead",
				name, w.Header().Get("Location"))
		}
		if loc := w.Header().Get("Location"); strings.Contains(loc, "evil.test") {
			t.Errorf("%s: Location points at the attacker: %q", name, loc)
		}
	}
}

// PKCE is mandatory, and plain is refused rather than tolerated — otherwise
// advertising only S256 would be a suggestion.
func TestAuthorizeRequiresS256(t *testing.T) {
	mux, _, _, cookie := oauthFlowMux(t)
	clientID := registerClient(t, mux)

	for name, mutate := range map[string]func(url.Values){
		"no challenge":    func(q url.Values) { q.Del("code_challenge") },
		"plain method":    func(q url.Values) { q.Set("code_challenge_method", "plain") },
		"missing method":  func(q url.Values) { q.Del("code_challenge_method") },
		"short challenge": func(q url.Values) { q.Set("code_challenge", "abc") },
	} {
		q := authorizeQuery(clientID)
		mutate(q)
		w := getWith(mux, oauth.PathAuthorize+"?"+q.Encode(), cookie)
		// Redirectable, so this one goes back to the client as an error — but
		// it must never reach the consent screen.
		if loc := w.Header().Get("Location"); strings.HasPrefix(loc, "/oauth/consent") {
			t.Errorf("%s: reached consent anyway", name)
		}
	}
}

// A wrong verifier must not exchange, or PKCE protects nothing.
func TestTokenRefusesAWrongVerifier(t *testing.T) {
	mux, code, clientID := approvedCode(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", testRedirect)
	form.Set("code_verifier", strings.Repeat("z", 64))
	if w := postForm(mux, oauth.PathToken, form); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier = %d, want 400", w.Code)
	}
}

// A code is bound to the redirect URI it was issued for.
func TestTokenRefusesADifferentRedirect(t *testing.T) {
	mux, code, clientID := approvedCode(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", "https://app.example.com/other")
	form.Set("code_verifier", testVerifier)
	if w := postForm(mux, oauth.PathToken, form); w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched redirect = %d, want 400", w.Code)
	}
}

// Replay: a second exchange fails AND takes the family with it, because a code
// presented twice means it leaked.
func TestReplayingACodeRevokesTheWholeGrant(t *testing.T) {
	mux, code, clientID := approvedCode(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", testRedirect)
	form.Set("code_verifier", testVerifier)

	w := postForm(mux, oauth.PathToken, form)
	if w.Code != http.StatusOK {
		t.Fatalf("first exchange = %d: %s", w.Code, w.Body.String())
	}
	var tok tokenResponse
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	if w := postForm(mux, oauth.PathToken, form); w.Code != http.StatusBadRequest {
		t.Fatalf("replay = %d, want 400", w.Code)
	}
	// The token issued by the FIRST exchange is now dead too — that is the
	// point of treating a replay as a compromise rather than a mistake.
	if r := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + tok.AccessToken}); r.Code != http.StatusUnauthorized {
		t.Fatalf("the first token still works after a replay: %d", r.Code)
	}
}

// Rotation: refreshing invalidates the old pair, so a stolen refresh token is
// usable at most once and its use is detectable.
func TestRefreshRotatesAndKillsTheOldToken(t *testing.T) {
	mux, code, clientID := approvedCode(t)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("redirect_uri", testRedirect)
	form.Set("code_verifier", testVerifier)
	w := postForm(mux, oauth.PathToken, form)
	var first tokenResponse
	_ = json.Unmarshal(w.Body.Bytes(), &first)

	rf := url.Values{}
	rf.Set("grant_type", "refresh_token")
	rf.Set("refresh_token", first.RefreshToken)
	rf.Set("client_id", clientID)
	w = postForm(mux, oauth.PathToken, rf)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", w.Code, w.Body.String())
	}
	var second tokenResponse
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if second.AccessToken == first.AccessToken || second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh returned the same credentials")
	}
	// Reusing the rotated refresh token fails.
	if w := postForm(mux, oauth.PathToken, rf); w.Code != http.StatusBadRequest {
		t.Errorf("reusing a rotated refresh token = %d, want 400", w.Code)
	}
	if r := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + second.AccessToken}); r.Code != http.StatusOK {
		t.Errorf("the rotated-in token does not work: %d", r.Code)
	}
}

// Declining reports access_denied to the client rather than silently doing
// nothing.
func TestDecliningConsentTellsTheClient(t *testing.T) {
	mux, _, _, cookie := oauthFlowMux(t)
	clientID := registerClient(t, mux)
	q := authorizeQuery(clientID)

	req := httptest.NewRequest("POST", oauth.PathConsent+"?"+q.Encode(),
		strings.NewReader(`{"approve":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("decline = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "access_denied") {
		t.Errorf("decline did not report access_denied: %s", w.Body.String())
	}
}

// Consent requires a session: a client cannot consent on its owner's behalf.
func TestConsentRequiresASession(t *testing.T) {
	mux, _, _, _ := oauthFlowMux(t)
	clientID := registerClient(t, mux)
	q := authorizeQuery(clientID)
	if w := getWith(mux, oauth.PathConsent+"?"+q.Encode(), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous consent view = %d, want 401", w.Code)
	}
}

// oauthFlowMux is a router with OAuth on and one signed-in user who can read
// "prod" — the person who does the consenting.
func oauthFlowMux(t *testing.T) (*http.ServeMux, *storagetest.Fake, *auth.Service, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	f := &storagetest.Fake{Tenants: []string{"prod", "payments"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "u1", Email: "person@x.io", Name: "Person", PasswordHash: h, Origin: "local"})
	_ = f.ReplaceAuthGrants(ctx, "u1",
		[]storage.AuthGrant{{UserID: "u1", Scope: "prod", Role: "editor"}})

	token, _, err := svc.Login(ctx, "person@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth:                     svc,
		Modules:                  modules.Set{modules.Core: true, modules.MCP: true},
		PublicURL:                testPublicURL,
		OAuthEnabled:             true,
		OAuthDynamicRegistration: true,
	})
	return mux, f, svc, &http.Cookie{Name: sessionCookieName, Value: token}
}

// approvedCode runs register → authorize → approve and returns the code.
func approvedCode(t *testing.T) (*http.ServeMux, string, string) {
	t.Helper()
	mux, _, _, cookie := oauthFlowMux(t)
	clientID := registerClient(t, mux)
	q := authorizeQuery(clientID)

	req := httptest.NewRequest("POST", oauth.PathConsent+"?"+q.Encode(),
		strings.NewReader(`{"approve":true,"project":"prod"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	var decided map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &decided)
	u, err := url.Parse(decided["redirect"])
	if err != nil {
		t.Fatal(err)
	}
	return mux, u.Query().Get("code"), clientID
}

// "*" is a role scope, not a project. Offering it would pin the token to a
// tenant literally named "*", which reads nothing — and would defeat the point
// of asking which project is being shared.
func TestConsentNeverOffersTheWildcardAsAProject(t *testing.T) {
	ctx := context.Background()
	f := &storagetest.Fake{Tenants: []string{"prod", "payments"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "adm", Email: "adm@x.io", Name: "Adm", PasswordHash: h, Origin: "local"})
	// A global admin: their only grant is the wildcard.
	_ = f.ReplaceAuthGrants(ctx, "adm",
		[]storage.AuthGrant{{UserID: "adm", Scope: "*", Role: "admin"}})
	token, _, err := svc.Login(ctx, "adm@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth: svc, Modules: modules.Set{modules.Core: true, modules.MCP: true},
		PublicURL: testPublicURL, OAuthEnabled: true, OAuthDynamicRegistration: true,
		Projects: []string{"prod", "payments"},
	})
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}
	clientID := registerClient(t, mux)

	w := getWith(mux, oauth.PathConsent+"?"+authorizeQuery(clientID).Encode(), cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("consent view = %d: %s", w.Code, w.Body.String())
	}
	var view consentView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	for _, p := range view.Projects {
		if p == "*" {
			t.Fatalf("the wildcard was offered as a project: %v", view.Projects)
		}
	}
	// A wildcard admin has no concrete grants, so the install's declared
	// projects are the choices — an empty list would silently share "default".
	if len(view.Projects) == 0 {
		t.Fatal("a global admin was offered no project at all")
	}
	if view.DefaultProject == "*" {
		t.Errorf("default project = %q", view.DefaultProject)
	}
}
