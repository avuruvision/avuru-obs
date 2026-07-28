package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// StartURL carries the authorize-endpoint redirect target and the PKCE verifier
// the caller must stash (in the login-state cookie) until the callback.
type StartURL struct{ URL, Verifier string }

// OIDCProvider wraps a discovered IdP for the authorization-code + PKCE flow.
type OIDCProvider struct {
	oauth       oauth2.Config
	verifier    *oidc.IDTokenVerifier
	groupsClaim string
}

// NewOIDCProvider performs OIDC discovery against cfg.Issuer and returns a
// provider bound to redirectURL (the hub's OIDC callback URL).
func NewOIDCProvider(ctx context.Context, cfg *OIDCConfig, redirectURL string) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &OIDCProvider{
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
			Endpoint: provider.Endpoint(), RedirectURL: redirectURL,
			Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"},
		},
		verifier:    provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		groupsClaim: cfg.GroupsClaim,
	}, nil
}

// Start builds the authorize-endpoint URL for the given CSRF state and OIDC
// nonce, generating a fresh PKCE verifier returned alongside it.
func (p *OIDCProvider) Start(state, nonce string) StartURL {
	verifier := oauth2.GenerateVerifier()
	url := p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return StartURL{URL: url, Verifier: verifier}
}

// Exchange swaps the callback code (with the stashed PKCE verifier) for tokens,
// verifies the ID token's signature/issuer/audience and the nonce, and returns
// the normalized identity.
func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier, nonce string) (ExternalIdentity, error) {
	tok, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return ExternalIdentity{}, fmt.Errorf("no id_token in response")
	}
	idTok, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idTok.Nonce != nonce {
		return ExternalIdentity{}, fmt.Errorf("nonce mismatch")
	}
	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		return ExternalIdentity{}, fmt.Errorf("decode claims: %w", err)
	}
	ext := ExternalIdentity{Subject: idTok.Subject}
	ext.Email, _ = claims["email"].(string)
	if n, ok := claims["name"].(string); ok {
		ext.Name = n
	} else {
		ext.Name = ext.Email
	}
	ext.Groups = stringSlice(claims[p.groupsClaim])
	return ext, nil
}

// stringSlice coerces a claim ([]any of strings, []string, or a single string) to []string.
func stringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		return []string{t}
	}
	return nil
}
