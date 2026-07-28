//go:build e2e

// OIDC SSO against the compose stack plus the OPT-IN mock IdP (compose
// profile `oidc-e2e` + deploy/compose/docker-compose.oidc-e2e.yaml). Skipped
// unless AVURUOPS_E2E_OIDC=1 — the default `make e2e` stack has no IdP.
//
// The flow under test is the hub's authorization-code + PKCE round trip:
//
//	GET /api/v1/auth/oidc/start    → 302 to the mock IdP's /authorize
//	GET <IdP>/authorize            → 302 back to the hub callback with a code
//	GET /api/v1/auth/oidc/callback → session cookie + 302 to /
//
// after which /api/v1/auth/me must report the identity and the group→grant
// mapping pinned in deploy/compose/oidc-e2e.yaml.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
	"time"
)

// The mock IdP's host as the HUB sees it (compose service DNS — the issuer in
// oidc-e2e.yaml) vs as THIS TEST reaches it (the published port). The hub's
// authorize redirect targets the former, which does not resolve on the host,
// so the client rewrites it before following — only the URL host changes;
// path, query, and the jar-managed cookies are untouched.
var (
	oidcInternalHost = envOr("AVURUOPS_E2E_OIDC_INTERNAL_HOST", "mock-oidc:8080")
	oidcExternalHost = envOr("AVURUOPS_E2E_OIDC_EXTERNAL_HOST", "localhost:18089")
)

// Mirrors deploy/compose/oidc-e2e.yaml (mapping) and the mock's JSON_CONFIG
// in docker-compose.yaml (claims) — the same fixture-pinning convention as
// adminPassword in auth_helpers_test.go.
const (
	oidcWantEmail = "sso-user@e2e"
	oidcWantScope = "staging"
	oidcWantRole  = "viewer"
)

func TestOIDCLoginMapsGrants(t *testing.T) {
	if os.Getenv("AVURUOPS_E2E_OIDC") != "1" {
		t.Skip("AVURUOPS_E2E_OIDC != 1 — mock-IdP stack not up (see deploy/compose/docker-compose.oidc-e2e.yaml)")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Redirects are followed manually: each hop may need its host rewritten,
	// and the walk must stop AT the hub callback rather than follow its final
	// 302 to /. ErrUseLastResponse only disables auto-following — the jar
	// still records every Set-Cookie, so the transient OIDC flow cookies
	// (Path=/api/v1/auth/oidc) and the session cookie survive intact.
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	final := driveOIDCFlow(t, client, hubURL+"/api/v1/auth/oidc/start")
	if final.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(final.Body, 2048))
		final.Body.Close()
		t.Fatalf("callback: %d (want 302), body: %s", final.StatusCode, body)
	}
	final.Body.Close()
	if loc := final.Header.Get("Location"); loc != "/" {
		t.Fatalf("callback redirects to %q, want /", loc)
	}

	// The session cookie now in the jar authenticates /auth/me.
	resp, err := client.Get(hubURL + "/api/v1/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/auth/me after SSO: %d, want 200", resp.StatusCode)
	}
	var me struct {
		User struct {
			Email     string `json:"email"`
			Anonymous bool   `json:"anonymous"`
		} `json:"user"`
		Grants []struct {
			Scope string `json:"scope"`
			Role  string `json:"role"`
		} `json:"grants"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decoding /auth/me: %v", err)
	}
	if me.User.Email != oidcWantEmail || me.User.Anonymous {
		t.Errorf("identity: email %q anonymous %v, want email %q anonymous false",
			me.User.Email, me.User.Anonymous, oidcWantEmail)
	}
	if len(me.Grants) != 1 || me.Grants[0].Scope != oidcWantScope || me.Grants[0].Role != oidcWantRole {
		t.Errorf("grants %+v, want exactly [{%s %s}] (deploy/compose/oidc-e2e.yaml mapping)",
			me.Grants, oidcWantScope, oidcWantRole)
	}
}

// driveOIDCFlow GETs startURL and walks the redirect chain (hub → IdP → hub
// callback), returning the CALLBACK response unfollowed (its body still open —
// the caller closes it). Defensive: if the mock serves its interactive login
// form instead of redirecting (interactiveLogin left at the standalone default
// of true), the form is submitted the way a browser would — a POST of
// `username` back to the same authorize URL.
func driveOIDCFlow(t *testing.T, client *http.Client, startURL string) *http.Response {
	t.Helper()
	hubU, err := url.Parse(hubURL)
	if err != nil {
		t.Fatal(err)
	}
	current, err := url.Parse(startURL)
	if err != nil {
		t.Fatal(err)
	}
	post := false
	for hop := 0; hop < 10; hop++ {
		var resp *http.Response
		var err error
		if post {
			resp, err = client.PostForm(current.String(), url.Values{"username": {"e2e-sso-user"}})
		} else {
			resp, err = client.Get(current.String())
		}
		post = false
		if err != nil {
			t.Fatalf("hop %d %s: %v", hop, current, err)
		}
		if current.Host == hubU.Host && current.Path == "/api/v1/auth/oidc/callback" {
			return resp // the flow's end state — the caller asserts on it
		}
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			next, err := current.Parse(loc)
			if err != nil {
				t.Fatalf("hop %d: unparsable Location %q: %v", hop, loc, err)
			}
			if next.Host == oidcInternalHost {
				next.Host = oidcExternalHost
			}
			current = next
		case resp.StatusCode == http.StatusOK && current.Host == oidcExternalHost:
			// The mock's interactive login page: same URL, form POST next.
			resp.Body.Close()
			post = true
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			t.Fatalf("hop %d %s: unexpected %d: %s", hop, current, resp.StatusCode, body)
		}
	}
	t.Fatal("OIDC flow did not reach the hub callback within 10 hops")
	return nil
}
