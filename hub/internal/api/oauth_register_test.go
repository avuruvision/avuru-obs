package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/oauth"
)

func postJSON(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// Registration is unauthenticated because a client that has never met this
// install has nothing to authenticate with. What makes that acceptable is the
// next test: it grants nothing.
func TestRegistrationIsOpenAndReturnsAPublicClient(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	w := postJSON(mux, oauth.PathRegister,
		`{"client_name":"Some Connector","redirect_uris":["https://app.example.com/cb"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("register = %d, want 201: %s", w.Code, w.Body.String())
	}
	var got oauth.RegistrationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ClientID == "" {
		t.Error("no client_id issued")
	}
	// Public client: a dynamic registrant has nowhere to keep a secret, and
	// PKCE is what protects the exchange instead.
	if got.TokenEndpointAuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method = %q, want none", got.TokenEndpointAuthMethod)
	}
	if strings.Contains(w.Body.String(), "client_secret") {
		t.Error("a client_secret was issued to a public client")
	}
}

// The claim that makes open registration safe: a registered client can read
// nothing until a person consents. There is no token anywhere in the response,
// and the client cannot call the resource.
func TestRegistrationGrantsNothing(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	w := postJSON(mux, oauth.PathRegister,
		`{"client_name":"Some Connector","redirect_uris":["https://app.example.com/cb"]}`)
	body := w.Body.String()
	for _, forbidden := range []string{"access_token", "avuruo_", "avurut_"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("registration handed out %q: %s", forbidden, body)
		}
	}
	var got oauth.RegistrationResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	// The client id alone reaches nothing.
	if r := mcpDo(mux, pingBody, map[string]string{"Authorization": "Bearer " + got.ClientID}); r.Code != http.StatusUnauthorized {
		t.Errorf("a client_id was accepted as a credential: %d", r.Code)
	}
}

func TestRegistrationRefusesWhatCannotBeTrusted(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	cases := map[string]string{
		"no name":              `{"redirect_uris":["https://a.example/cb"]}`,
		"no redirect":          `{"client_name":"X"}`,
		"plaintext redirect":   `{"client_name":"X","redirect_uris":["http://a.example/cb"]}`,
		"loopback redirect":    `{"client_name":"X","redirect_uris":["http://127.0.0.1:9000/cb"]}`,
		"fragment in redirect": `{"client_name":"X","redirect_uris":["https://a.example/cb#f"]}`,
		"confidential client":  `{"client_name":"X","redirect_uris":["https://a.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"unknown grant":        `{"client_name":"X","redirect_uris":["https://a.example/cb"],"grant_types":["password"]}`,
	}
	for name, body := range cases {
		w := postJSON(mux, oauth.PathRegister, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400 (%s)", name, w.Code, w.Body.String())
		}
		// RFC 6749 shape, which is what a client parses.
		if !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: response is not an OAuth error: %s", name, w.Body.String())
		}
	}
}

// Six redirect URIs is over the bound; abuse should cost a 400, not a row.
func TestRegistrationBoundsTheRedirectList(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	var uris []string
	for i := 0; i < oauth.MaxRedirectURIs+1; i++ {
		uris = append(uris, `"https://a.example/cb`+string(rune('a'+i))+`"`)
	}
	body := `{"client_name":"X","redirect_uris":[` + strings.Join(uris, ",") + `]}`
	if w := postJSON(mux, oauth.PathRegister, body); w.Code != http.StatusBadRequest {
		t.Fatalf("too many redirect_uris = %d, want 400", w.Code)
	}
}

// The endpoint is open, so the rate limit is what stops it being a free write
// endpoint for anyone who can reach the hub.
func TestRegistrationIsRateLimited(t *testing.T) {
	mux, _, _ := oauthMux(t, oauth.ScopeMCPRead, oauth.ResourceURI(testPublicURL))
	body := `{"client_name":"X","redirect_uris":["https://a.example/cb"]}`
	var limited bool
	for i := 0; i < 40; i++ {
		if postJSON(mux, oauth.PathRegister, body).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("registration was never rate limited")
	}
}

// An operator who turns registration off gets a 404 and a metadata document
// that no longer advertises it — a client is told the truth in both places.
func TestRegistrationCanBeTurnedOff(t *testing.T) {
	mux, _, _ := oauthMuxWith(t, func(c *Config) { c.OAuthDynamicRegistration = false })
	if w := postJSON(mux, oauth.PathRegister,
		`{"client_name":"X","redirect_uris":["https://a.example/cb"]}`); w.Code != http.StatusNotFound {
		t.Errorf("register with DCR off = %d, want 404", w.Code)
	}
	w := authDo(mux, "GET", oauth.PathWellKnownAS, nil, nil)
	if strings.Contains(w.Body.String(), "registration_endpoint") {
		t.Errorf("metadata still advertises registration: %s", w.Body.String())
	}
}
