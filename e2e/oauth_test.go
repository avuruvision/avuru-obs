//go:build e2e

// The OAuth 2.1 authorization server against a real stack: register, consent,
// exchange, and then read the estate with what came out.
//
// The consent DECISION is posted straight to the hub API rather than driven
// through a browser: compose publishes the hub and the UI on two different
// origins, so a Go client pretending to be a browser would be fighting the
// CSRF origin check rather than testing it. The consent SCREEN is covered by
// ui/e2e/oauth-consent.spec.ts.
package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	oauthRedirect = "https://app.example.com/cb"
	oauthVerifier = "0123456789012345678901234567890123456789012345678901234567890123"
)

func oauthChallenge() string {
	sum := sha256.Sum256([]byte(oauthVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthOn reports whether this stack runs the authorization server. The
// compose sandbox does not set a public URL, so OAuth is off there and these
// cases skip rather than fail — the flow needs an absolute issuer to exist at
// all, which is the same reason the chart refuses to render without one.
func oauthOn(t *testing.T) bool {
	t.Helper()
	resp, err := apiClient.Get(hubURL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Discovery is the bootstrap: a client with no credential must be able to read
// where to get one.
func TestOAuthDiscoveryIsReachableWithoutACredential(t *testing.T) {
	if !oauthOn(t) {
		t.Skip("OAuth is not enabled on this stack (no public URL configured)")
	}
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
	} {
		// A bare client, carrying none of the suite's admin cookies.
		resp, err := http.Get(hubURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s unauthenticated = %s", path, resp.Status)
		}
		resp.Body.Close()
	}
}

// An unauthenticated /mcp call must carry the challenge that tells a client
// where to authenticate — without it a connector is stuck.
func TestMCPChallengePointsAtTheMetadata(t *testing.T) {
	if !oauthOn(t) {
		t.Skip("OAuth is not enabled on this stack")
	}
	resp, err := http.Post(hubURL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp = %s, want 401", resp.Status)
	}
	if ch := resp.Header.Get("WWW-Authenticate"); !strings.Contains(ch, "resource_metadata=") {
		t.Fatalf("challenge = %q, want a resource_metadata pointer", ch)
	}
}

// Register → consent → exchange → call. The whole point, against a real hub
// and a real ClickHouse.
func TestOAuthFlowIssuesAWorkingToken(t *testing.T) {
	if !oauthOn(t) {
		t.Skip("OAuth is not enabled on this stack")
	}
	// 1. Register, unauthenticated, as a hosted client would.
	regBody := `{"client_name":"e2e connector","redirect_uris":["` + oauthRedirect + `"],"scope":"mcp:read offline_access"}`
	resp, err := http.Post(hubURL+"/api/v1/auth/oauth/register", "application/json", strings.NewReader(regBody))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	if reg.ClientID == "" {
		t.Fatal("registration issued no client_id")
	}

	// 2. Consent, as the signed-in admin (apiClient carries the session).
	q := url.Values{}
	q.Set("client_id", reg.ClientID)
	q.Set("redirect_uri", oauthRedirect)
	q.Set("response_type", "code")
	q.Set("code_challenge", oauthChallenge())
	q.Set("code_challenge_method", "S256")
	q.Set("scope", "mcp:read offline_access")
	q.Set("state", "e2e")

	req, _ := http.NewRequest("POST", hubURL+"/api/v1/auth/oauth/consent?"+q.Encode(),
		strings.NewReader(`{"approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = apiClient.Do(req)
	if err != nil {
		t.Fatalf("consent: %v", err)
	}
	var decided struct {
		Redirect string `json:"redirect"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&decided)
	resp.Body.Close()
	redirect, err := url.Parse(decided.Redirect)
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("consent produced no code: %q", decided.Redirect)
	}

	// 3. Exchange, with the PKCE verifier.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", redirect.Query().Get("code"))
	form.Set("client_id", reg.ClientID)
	form.Set("redirect_uri", oauthRedirect)
	form.Set("code_verifier", oauthVerifier)
	resp, err = http.PostForm(hubURL+"/api/v1/auth/oauth/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if tok.AccessToken == "" {
		t.Fatal("no access token issued")
	}

	// 4. And read the estate with it.
	req, _ = http.NewRequest("POST", hubURL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list with an OAuth token = %s", resp.Status)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["result"]; !ok {
		t.Fatalf("no result in %v", out)
	}

	// 5. And must NOT be a credential for the rest of the API.
	req, _ = http.NewRequest("GET", hubURL+"/api/v1/services", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an MCP token reached /api/v1/services: %s", resp.Status)
	}
}
