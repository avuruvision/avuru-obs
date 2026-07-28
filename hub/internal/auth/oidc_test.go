package auth

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/oauth2-proxy/mockoidc"
)

// TestOIDCProviderCodeFlow drives the full authorization-code + PKCE flow
// against an in-process mock IdP: discovery, Start (authorize URL + verifier),
// the authorize redirect (GET, capture ?code=), then Exchange, and asserts the
// normalized identity (subject/email/groups) round-trips through the verified
// ID token.
func TestOIDCProviderCodeFlow(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc.Run: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	m.QueueUser(&mockoidc.MockUser{
		Subject:           "kc|1",
		Email:             "a@x.io",
		PreferredUsername: "A",
		Groups:            []string{"obs-admins"},
	})

	ctx := context.Background()
	cfg := &OIDCConfig{
		Issuer:       m.Issuer(),
		ClientID:     m.ClientID,
		ClientSecret: m.ClientSecret,
		GroupsClaim:  "groups",
	}
	const redirectURL = "http://127.0.0.1/callback"
	p, err := NewOIDCProvider(ctx, cfg, redirectURL)
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}

	const (
		state = "state-123"
		nonce = "nonce-abc"
	)
	start := p.Start(state, nonce)
	if start.URL == "" || start.Verifier == "" {
		t.Fatalf("Start returned empty fields: %+v", start)
	}

	// Follow no redirects: the mock IdP auto-consents (a user is queued) and
	// 302s straight to redirectURL?code=...&state=...
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(start.URL)
	if err != nil {
		t.Fatalf("GET authorize URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatalf("authorize did not redirect (status %d, no Location)", resp.StatusCode)
	}
	redirect, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	if got := redirect.Query().Get("state"); got != state {
		t.Fatalf("state mismatch: got %q, want %q", got, state)
	}
	code := redirect.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %q", loc)
	}

	ext, err := p.Exchange(ctx, code, start.Verifier, nonce)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if ext.Subject != "kc|1" {
		t.Errorf("Subject: got %q, want %q", ext.Subject, "kc|1")
	}
	if ext.Email != "a@x.io" {
		t.Errorf("Email: got %q, want %q", ext.Email, "a@x.io")
	}
	if !reflect.DeepEqual(ext.Groups, []string{"obs-admins"}) {
		t.Errorf("Groups: got %v, want %v", ext.Groups, []string{"obs-admins"})
	}
}

// TestOIDCExchangeRejectsNonceMismatch proves the callback fails closed when the
// ID token's nonce does not match the one bound at Start — an anti-replay guard.
func TestOIDCExchangeRejectsNonceMismatch(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc.Run: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	m.QueueUser(&mockoidc.MockUser{Subject: "kc|2", Email: "b@x.io", Groups: []string{"obs-viewers"}})

	ctx := context.Background()
	cfg := &OIDCConfig{Issuer: m.Issuer(), ClientID: m.ClientID, ClientSecret: m.ClientSecret, GroupsClaim: "groups"}
	p, err := NewOIDCProvider(ctx, cfg, "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}

	start := p.Start("state-x", "real-nonce")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(start.URL)
	if err != nil {
		t.Fatalf("GET authorize URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	redirect, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := redirect.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %q", resp.Header.Get("Location"))
	}

	// Verify with the WRONG nonce — must be rejected.
	if _, err := p.Exchange(ctx, code, start.Verifier, "attacker-nonce"); err == nil {
		t.Fatal("Exchange accepted a mismatched nonce")
	}
}
