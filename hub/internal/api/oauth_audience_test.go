package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

const testPublicURL = "https://obs.example.com"

// errBoom stands in for any store failure that is not ErrNotFound.
var errBoom = errors.New("clickhouse is down")

// oauthMux builds a router with the MCP module on, OAuth on, and one live
// access token belonging to u2 (editor on prod), minted for the MCP resource.
// oauthMuxWith is oauthMux with the config adjusted — for the cases that are
// about an operator's switch rather than a credential.
func oauthMuxWith(t *testing.T, tweak func(*Config)) (*http.ServeMux, *storagetest.Fake, string) {
	t.Helper()
	return oauthMuxCfg(t, oauth.ScopeMCPRead, ResourceURIForTest(), tweak)
}

// ResourceURIForTest keeps the audience string in one place across the tests.
func ResourceURIForTest() string { return oauth.ResourceURI(testPublicURL) }

func oauthMux(t *testing.T, scope string, resource string) (*http.ServeMux, *storagetest.Fake, string) {
	t.Helper()
	return oauthMuxCfg(t, scope, resource, nil)
}

func oauthMuxCfg(t *testing.T, scope, resource string, tweak func(*Config)) (*http.ServeMux, *storagetest.Fake, string) {
	t.Helper()
	ctx := context.Background()
	f := &storagetest.Fake{Tenants: []string{"payments", "prod"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")

	_ = f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "u2", Email: "bot@x.io", Name: "Bot", PasswordHash: h, Origin: "local"})
	_ = f.ReplaceAuthGrants(ctx, "u2",
		[]storage.AuthGrant{{UserID: "u2", Scope: "prod", Role: "editor"}})

	_ = f.CreateOAuthGrant(ctx, storage.OAuthGrant{
		GrantID: "g1", ClientID: "c1", UserID: "u2",
		Scope: scope, Project: "prod", Resource: resource, CreatedAt: time.Now()})

	raw, hash := auth.NewOAuthToken(auth.OAuthAccessPrefix)
	_ = f.CreateOAuthToken(ctx, storage.OAuthToken{
		TokenHash: hash, Kind: storage.OAuthTokenAccess, GrantID: "g1", ClientID: "c1",
		UserID: "u2", Resource: resource, Scope: scope, Project: "prod",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()})

	cfg := Config{
		Auth:                     svc,
		Modules:                  modules.Set{modules.Core: true, modules.MCP: true},
		PublicURL:                testPublicURL,
		OAuthEnabled:             true,
		OAuthDynamicRegistration: true,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, cfg)
	return mux, f, raw
}

// mcpDo posts a JSON-RPC body. authDo always sends "{}", which /mcp rejects
// before it ever reaches the auth decision under test.
func mcpDo(mux *http.ServeMux, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

const pingBody = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

// A token minted for the MCP resource is not a credential for the rest of the
// API.
//
// Note what this does and does not prove. Today it would also pass without the
// prefix refusal, because an OAuth token's hash lives in oauth_token and
// IdentityFromAPIToken reads auth_token — so the lookup misses and 401s anyway.
// It asserts the OUTCOME. TestOAuthTokenIsRefusedEvenIfTheLookupWouldSucceed
// below asserts the MECHANISM, which is the part that has to survive a future
// change to how credentials are stored.
func TestOAuthTokenIsRefusedOnTheOrdinaryAPI(t *testing.T) {
	mux, _, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))

	for _, path := range []string{
		"/api/v1/services",
		"/api/v1/traces",
		"/api/v1/projects",
		"/api/v1/auth/me",
	} {
		hdr := map[string]string{"Authorization": "Bearer " + raw, "X-Avuru-Tenant": "prod"}
		w := authDo(mux, "GET", path, nil, hdr)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s with an MCP token = %d, want 401", path, w.Code)
		}
	}
}

// The mechanism, pinned.
//
// The refusal is a PREFIX check in the shared path, not a consequence of where
// the row happens to live. This test makes the ordinary lookup succeed for an
// avuruo_ token — the situation a future refactor that unified the credential
// tables would create — and requires a 401 regardless.
//
// Without the prefix check this test fails with a 200, which is precisely the
// hole it exists to keep shut: an MCP credential answering for the whole API.
func TestOAuthTokenIsRefusedEvenIfTheLookupWouldSucceed(t *testing.T) {
	ctx := context.Background()
	mux, f, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))

	// Same secret, also registered as a personal API token for a user who can
	// read prod. Contrived on purpose: it is the shape of the mistake, not a
	// state the product produces today.
	_ = f.CreateAuthToken(ctx, storage.AuthToken{
		TokenHash: auth.HashAPIToken(raw), UserID: "u2", Name: "collision",
		Prefix: raw[:12], CreatedAt: time.Now()})

	hdr := map[string]string{"Authorization": "Bearer " + raw, "X-Avuru-Tenant": "prod"}
	if w := authDo(mux, "GET", "/api/v1/services", nil, hdr); w.Code != http.StatusUnauthorized {
		t.Fatalf("an MCP credential reached the ordinary API: %d, want 401", w.Code)
	}
}

// The same token DOES work against the resource it was minted for — otherwise
// the test above would pass for the boring reason that nothing works.
func TestOAuthTokenWorksOnItsOwnResource(t *testing.T) {
	mux, _, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /mcp with its own token = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A token whose stored audience is some other resource must not be accepted
// here either — the check compares the row, not anything the caller supplied.
func TestOAuthTokenForAnotherResourceIsRefused(t *testing.T) {
	mux, _, raw := oauthMux(t, oauth.ScopeMCPRead, "https://elsewhere.example.com/mcp")
	w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("cross-resource token = %d, want 401", w.Code)
	}
}

// Without mcp:read the token authenticates but authorizes nothing.
func TestOAuthTokenWithoutTheScopeIsForbidden(t *testing.T) {
	mux, _, raw := oauthMux(t, oauth.ScopeOfflineAccess, oauth.ResourceURI(testPublicURL))
	w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw})
	if w.Code != http.StatusForbidden {
		t.Fatalf("token without mcp:read = %d, want 403", w.Code)
	}
}

// The 401 must carry the discovery bootstrap. A client that has never seen this
// server reads resource_metadata and finds its way from there; without it a
// connector gets a bare 401 and has nowhere to go.
func TestUnauthenticatedMCPCarriesTheChallenge(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	w := mcpDo(mux, pingBody, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp = %d, want 401", w.Code)
	}
	got := w.Header().Get("WWW-Authenticate")
	if got == "" {
		t.Fatal("no WWW-Authenticate challenge on the 401")
	}
	for _, want := range []string{"Bearer", "resource_metadata=", oauth.PathWellKnownPRMCP} {
		if !strings.Contains(got, want) {
			t.Errorf("challenge %q is missing %q", got, want)
		}
	}
}

// Revoking the consent kills the token on the NEXT request, because
// authorization is read from rows rather than carried in a claim. This is the
// whole argument for opaque tokens over JWTs.
func TestRevokingTheGrantStopsTheTokenImmediately(t *testing.T) {
	mux, f, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	hdr := map[string]string{"Authorization": "Bearer " + raw}

	if w := mcpDo(mux, pingBody, hdr); w.Code != http.StatusOK {
		t.Fatalf("precondition: token should work, got %d", w.Code)
	}
	if err := f.RevokeOAuthGrant(context.Background(), "u2", "g1"); err != nil {
		t.Fatal(err)
	}
	if w := mcpDo(mux, pingBody, hdr); w.Code != http.StatusUnauthorized {
		t.Fatalf("after revoking the grant = %d, want 401", w.Code)
	}
}

// Disabling the owner does the same, for the same reason: no grant is ever read
// off the token row.
func TestDisablingTheOwnerStopsTheToken(t *testing.T) {
	mux, f, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	hdr := map[string]string{"Authorization": "Bearer " + raw}

	u, _ := f.GetAuthUser(context.Background(), "u2")
	u.Disabled = true
	_ = f.SaveAuthUser(context.Background(), u)

	if w := mcpDo(mux, pingBody, hdr); w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner = %d, want 401", w.Code)
	}
}

// A refresh token is not an access token and must not be honoured as one.
func TestRefreshTokenIsNotAnAccessToken(t *testing.T) {
	ctx := context.Background()
	mux, f, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	raw, hash := auth.NewOAuthToken(auth.OAuthRefreshPrefix)
	_ = f.CreateOAuthToken(ctx, storage.OAuthToken{
		TokenHash: hash, Kind: storage.OAuthTokenRefresh, GrantID: "g1", ClientID: "c1",
		UserID: "u2", Resource: oauth.ResourceURI(testPublicURL),
		Scope: oauth.ScopeMCPRead, Project: "prod",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()})

	w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh token used as access = %d, want 401", w.Code)
	}
}

// A store failure is 503, never 401: telling every automated caller to
// re-authenticate against a hub that is merely ill is how an outage becomes a
// credential rotation.
func TestStoreFailureIsNotAnAuthFailure(t *testing.T) {
	mux, f, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	f.OAuthErr = errBoom
	w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("store failure = %d, want 503", w.Code)
	}
}

// Discovery must work for a caller with NO credential — finding out where to
// get one is its whole purpose.
func TestDiscoveryMetadataIsUnauthenticated(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	for _, path := range []string{
		oauth.PathWellKnownPR, oauth.PathWellKnownPRMCP, oauth.PathWellKnownAS,
	} {
		w := authDo(mux, "GET", path, nil, nil)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s unauthenticated = %d, want 200", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), testPublicURL) {
			t.Errorf("GET %s does not carry the public URL: %s", path, w.Body.String())
		}
	}
}

// With the authorization server off, the documents are absent rather than
// describing a flow this install does not run — which would send every
// connector down a dead end.
func TestDiscoveryMetadataAbsentWhenOAuthIsOff(t *testing.T) {
	ctx := context.Background()
	f := &storagetest.Fake{Tenants: []string{"prod"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(ctx, storage.AuthUser{ID: "u2", Email: "b@x.io", PasswordHash: h, Origin: "local"})

	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth:      svc,
		Modules:   modules.Set{modules.Core: true, modules.MCP: true},
		PublicURL: testPublicURL,
		// OAuthEnabled deliberately false.
	})
	for _, path := range []string{oauth.PathWellKnownPR, oauth.PathWellKnownAS} {
		if w := authDo(mux, "GET", path, nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s with OAuth off = %d, want 404", path, w.Code)
		}
	}
}

// The step-1 guarantee: turning OAuth on must not disturb the credential the
// MCP server already accepted.
func TestAPITokenStillReachesMCPWithOAuthOn(t *testing.T) {
	ctx := context.Background()
	f := &storagetest.Fake{Tenants: []string{"prod"}}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	h, _ := auth.HashPassword("pw")
	_ = f.SaveAuthUser(ctx, storage.AuthUser{ID: "u2", Email: "b@x.io", PasswordHash: h, Origin: "local"})
	_ = f.ReplaceAuthGrants(ctx, "u2", []storage.AuthGrant{{UserID: "u2", Scope: "prod", Role: "editor"}})
	raw, prefix, hash := auth.NewAPIToken()
	_ = f.CreateAuthToken(ctx, storage.AuthToken{
		TokenHash: hash, UserID: "u2", Name: "ci", Prefix: prefix, CreatedAt: time.Now()})

	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{
		Auth: svc, Modules: modules.Set{modules.Core: true, modules.MCP: true},
		PublicURL: testPublicURL, OAuthEnabled: true,
	})
	// An API token carries no project of its own, so it names one the way it
	// always has. (An OAuth token does not need this: the project it was
	// consented to is pinned on the token, which is the point.)
	w := mcpDo(mux, pingBody, map[string]string{
		"Authorization": "Bearer " + raw, "X-Avuru-Tenant": "prod"})
	if w.Code != http.StatusOK {
		t.Fatalf("an API token stopped working when OAuth was enabled: %d", w.Code)
	}
}

// The project pin. A hosted client cannot set X-Avuru-Tenant, so without this
// every connector would read the default project and be useless on a
// multi-project install.
func TestOAuthTokenReadsTheProjectItWasConsentedTo(t *testing.T) {
	mux, _, raw := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	// No tenant header at all: the token says "prod", and u2 can only read prod.
	if w := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + raw}); w.Code != http.StatusOK {
		t.Fatalf("no header = %d, want 200 (the token's project should apply)", w.Code)
	}
	// Agreeing with the token is fine.
	if w := mcpDo(mux, pingBody, map[string]string{
		"Authorization": "Bearer " + raw, "X-Avuru-Tenant": "prod"}); w.Code != http.StatusOK {
		t.Errorf("matching header = %d, want 200", w.Code)
	}
	// Disagreeing is refused rather than silently overridden: the token records
	// what a person consented to, and a header cannot move it elsewhere.
	if w := mcpDo(mux, pingBody, map[string]string{
		"Authorization": "Bearer " + raw, "X-Avuru-Tenant": "payments"}); w.Code != http.StatusForbidden {
		t.Errorf("conflicting header = %d, want 403", w.Code)
	}
}
