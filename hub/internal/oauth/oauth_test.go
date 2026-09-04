package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// The resource URI is the audience an access token is stamped with, the
// `resource` in the metadata document, and the string /mcp compares against.
// One function so those three cannot disagree — an audience check comparing
// differently-normalised strings is a check that passes when it should not.
func TestResourceURIIsStableAcrossTrailingSlashes(t *testing.T) {
	for _, in := range []string{
		"https://obs.example.com",
		"https://obs.example.com/",
		"  https://obs.example.com  ",
	} {
		if got := ResourceURI(in); got != "https://obs.example.com/mcp" {
			t.Errorf("ResourceURI(%q) = %q", in, got)
		}
	}
	if got := ResourceURI(""); got != "" {
		t.Errorf("ResourceURI(\"\") = %q, want empty", got)
	}
}

func TestIssuerHasNoPathAndNoTrailingSlash(t *testing.T) {
	if got := Issuer("https://obs.example.com/"); got != "https://obs.example.com" {
		t.Errorf("Issuer = %q", got)
	}
}

// Exactly S256, never plain. Advertising plain is the downgrade vector: a
// client that would have used S256 can be talked into plain by a metadata
// document it trusts.
func TestMetadataAdvertisesOnlyS256(t *testing.T) {
	m := NewAuthorizationServer("https://obs.example.com", true)
	if len(m.CodeChallengeMethodsSupported) != 1 || m.CodeChallengeMethodsSupported[0] != "S256" {
		t.Fatalf("code_challenge_methods_supported = %v, want [S256]", m.CodeChallengeMethodsSupported)
	}
	for _, v := range m.CodeChallengeMethodsSupported {
		if strings.EqualFold(v, "plain") {
			t.Fatal("plain must never be advertised")
		}
	}
}

// Every advertised endpoint must be absolute: a client fetches them from
// somewhere else entirely.
func TestMetadataURLsAreAbsolute(t *testing.T) {
	m := NewAuthorizationServer("https://obs.example.com", true)
	for name, got := range map[string]string{
		"authorization": m.AuthorizationEndpoint,
		"token":         m.TokenEndpoint,
		"registration":  m.RegistrationEndpoint,
		"revocation":    m.RevocationEndpoint,
	} {
		if !strings.HasPrefix(got, "https://obs.example.com/") {
			t.Errorf("%s endpoint = %q, want absolute", name, got)
		}
	}
}

// Registration is only advertised when the operator left it on: a client that
// cannot register should not be told it can.
func TestRegistrationEndpointFollowsTheSwitch(t *testing.T) {
	if m := NewAuthorizationServer("https://o.example", false); m.RegistrationEndpoint != "" {
		t.Errorf("registration advertised while disabled: %q", m.RegistrationEndpoint)
	}
}

func TestProtectedResourcePointsAtTheAuthorizationServer(t *testing.T) {
	m := NewProtectedResource("https://obs.example.com")
	if m.Resource != "https://obs.example.com/mcp" {
		t.Errorf("resource = %q", m.Resource)
	}
	if len(m.AuthorizationServers) != 1 || m.AuthorizationServers[0] != "https://obs.example.com" {
		t.Errorf("authorization_servers = %v", m.AuthorizationServers)
	}
}

// Exact string match only. A prefix rule is how an open redirect gets built:
// https://app.example.com/cb would otherwise accept
// https://app.example.com/cb.evil.test, and the authorization code goes
// wherever this says it goes.
func TestRedirectURIMatchingIsExact(t *testing.T) {
	registered := []string{"https://app.example.com/cb"}
	cases := map[string]bool{
		"https://app.example.com/cb":           true,
		"https://app.example.com/cb/":          false,
		"https://app.example.com/cb.evil.test": false,
		"https://app.example.com/cb?x=1":       false,
		"https://app.example.com:443/cb":       false,
		"https://evil.test/cb":                 false,
		"https://app.example.com/cb#f":         false,
		"HTTPS://APP.EXAMPLE.COM/cb":           false,
	}
	for uri, want := range cases {
		if got := MatchRedirectURI(registered, uri); got != want {
			t.Errorf("MatchRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestValidRedirectURIRefusesWhatCannotBeTrusted(t *testing.T) {
	cases := map[string]bool{
		"https://app.example.com/cb":       true,
		"http://app.example.com/cb":        false, // plaintext
		"http://127.0.0.1:1234/cb":         false, // loopback is a later decision
		"https://user:pw@app.example.com/": false, // userinfo
		"https://app.example.com/cb#frag":  false, // fragment
		"not a url at all":                 false,
		"":                                 false,
	}
	for uri, want := range cases {
		if got := ValidRedirectURI(uri); got != want {
			t.Errorf("ValidRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestPKCEVerifiesOnlyTheRightVerifier(t *testing.T) {
	verifier := strings.Repeat("a", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if !VerifyPKCE(challenge, verifier) {
		t.Fatal("the correct verifier was rejected")
	}
	if VerifyPKCE(challenge, strings.Repeat("b", 64)) {
		t.Error("a wrong verifier was accepted")
	}
	// A short verifier is brute-forceable, which would undo the point of PKCE.
	if VerifyPKCE(challenge, "tooshort") {
		t.Error("a verifier below the RFC 7636 minimum was accepted")
	}
	if VerifyPKCE("", verifier) {
		t.Error("an empty challenge was accepted")
	}
}

func TestScopeNormalisationKeepsOnlyWhatWeIssue(t *testing.T) {
	got := NormalizeScope("mcp:read admin:write mcp:read offline_access")
	if got != "mcp:read offline_access" {
		t.Errorf("NormalizeScope = %q", got)
	}
	if HasScope(got, "admin:write") {
		t.Error("an unissued scope survived")
	}
}
