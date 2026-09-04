package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/oauth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// handleToken exchanges an authorization code, or rotates a refresh token.
//
// Form-encoded per RFC 6749, and answered with Cache-Control: no-store so a
// proxy never keeps a credential.
func (a *API) handleToken(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return writeOAuthError(http.StatusBadRequest,
			&oauth.Error{Code: oauth.ErrInvalidRequest, Description: "malformed form body"})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		return a.exchangeCode(w, r)
	case "refresh_token":
		return a.rotateRefresh(w, r)
	default:
		return writeOAuthError(http.StatusBadRequest, &oauth.Error{
			Code:        oauth.ErrUnsupportedGrantType,
			Description: "grant_type must be authorization_code or refresh_token",
		})
	}
}

func (a *API) exchangeCode(w http.ResponseWriter, r *http.Request) error {
	f := r.PostForm
	rawCode := f.Get("code")
	if rawCode == "" {
		return writeOAuthError(http.StatusBadRequest,
			&oauth.Error{Code: oauth.ErrInvalidRequest, Description: "code is required"})
	}
	st, err := a.store()
	if err != nil {
		return err
	}
	hash := auth.HashAPIToken(rawCode)
	code, err := st.GetOAuthAuthCode(r.Context(), hash)
	if errors.Is(err, storage.ErrNotFound) {
		return invalidGrant("the code is unknown")
	}
	if err != nil {
		return err
	}

	// REPLAY. A code presented twice means it leaked, so the safe response is
	// not merely to refuse this exchange but to invalidate everything already
	// issued from it — OAuth 2.1's rule, and the reason the family can be
	// revoked in one call.
	if code.Consumed || !claimCode(hash) {
		_ = st.RevokeOAuthTokensForGrant(r.Context(), code.GrantID)
		_ = st.RevokeOAuthGrant(r.Context(), code.UserID, code.GrantID)
		return invalidGrant("this code has already been used; the grant it belonged to has been revoked")
	}
	if !code.ExpiresAt.IsZero() && code.ExpiresAt.Before(time.Now()) {
		return invalidGrant("the code has expired")
	}
	// Bound to the client and the exact redirect URI it was issued for, so a
	// code observed in transit cannot be redeemed by anyone else.
	if f.Get("client_id") != code.ClientID {
		return invalidGrant("the code was not issued to this client")
	}
	if f.Get("redirect_uri") != code.RedirectURI {
		return invalidGrant("redirect_uri does not match the one the code was issued for")
	}
	if res := f.Get("resource"); res != "" && res != code.Resource {
		return invalidGrant("resource does not match the one the code was issued for")
	}
	if !oauth.VerifyPKCE(code.Challenge, f.Get("code_verifier")) {
		return invalidGrant("code_verifier does not match the challenge")
	}
	if err := st.ConsumeOAuthAuthCode(r.Context(), hash); err != nil {
		return invalidGrant("this code has already been used")
	}

	return a.issuePair(w, r, code.GrantID, code.ClientID, code.UserID,
		code.Resource, code.Scope, code.Project)
}

func (a *API) rotateRefresh(w http.ResponseWriter, r *http.Request) error {
	f := r.PostForm
	raw := f.Get("refresh_token")
	if raw == "" {
		return writeOAuthError(http.StatusBadRequest,
			&oauth.Error{Code: oauth.ErrInvalidRequest, Description: "refresh_token is required"})
	}
	st, err := a.store()
	if err != nil {
		return err
	}
	hash := auth.HashAPIToken(raw)
	tok, err := st.GetOAuthTokenByHash(r.Context(), hash)
	if errors.Is(err, storage.ErrNotFound) {
		// Either never existed or already rotated. A rotated token being
		// presented is a replay, and the family it belonged to is already gone
		// — there is nothing further to revoke, so this is a plain refusal.
		return invalidGrant("the refresh token is unknown or has already been used")
	}
	if err != nil {
		return err
	}
	if tok.Kind != storage.OAuthTokenRefresh {
		return invalidGrant("that is not a refresh token")
	}
	if !tok.ExpiresAt.IsZero() && tok.ExpiresAt.Before(time.Now()) {
		return invalidGrant("the refresh token has expired")
	}
	if f.Get("client_id") != tok.ClientID {
		return invalidGrant("the refresh token was not issued to this client")
	}
	// Rotation: the old pair dies with the new one's birth, so a stolen
	// refresh token is usable at most once and its use is detectable.
	if err := st.RevokeOAuthTokensForGrant(r.Context(), tok.GrantID); err != nil {
		return err
	}
	return a.issuePair(w, r, tok.GrantID, tok.ClientID, tok.UserID,
		tok.Resource, tok.Scope, tok.Project)
}

func (a *API) issuePair(w http.ResponseWriter, r *http.Request, grantID, clientID, userID, resource, scope, project string) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	now := time.Now()
	rawAccess, accessHash := auth.NewOAuthToken(auth.OAuthAccessPrefix)
	if err := st.CreateOAuthToken(r.Context(), storage.OAuthToken{
		TokenHash: accessHash, Kind: storage.OAuthTokenAccess, GrantID: grantID,
		ClientID: clientID, UserID: userID, Resource: resource, Scope: scope,
		Project: project, ExpiresAt: now.Add(oauth.AccessTokenTTL), CreatedAt: now,
	}); err != nil {
		return err
	}
	resp := tokenResponse{
		AccessToken: rawAccess,
		TokenType:   "Bearer",
		ExpiresIn:   int(oauth.AccessTokenTTL.Seconds()),
		Scope:       scope,
	}
	// A refresh token only when it was asked for. A client that did not request
	// offline_access should not be handed a long-lived credential it never
	// wanted and may not store carefully.
	if oauth.HasScope(scope, oauth.ScopeOfflineAccess) {
		rawRefresh, refreshHash := auth.NewOAuthToken(auth.OAuthRefreshPrefix)
		if err := st.CreateOAuthToken(r.Context(), storage.OAuthToken{
			TokenHash: refreshHash, Kind: storage.OAuthTokenRefresh, GrantID: grantID,
			ClientID: clientID, UserID: userID, Resource: resource, Scope: scope,
			Project: project, ExpiresAt: now.Add(oauth.RefreshTokenTTL), CreatedAt: now,
		}); err != nil {
			return err
		}
		resp.RefreshToken = rawRefresh
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleRevoke implements RFC 7009. Always 200, whatever was passed: telling a
// caller whether a token existed would make this an oracle, and the client's
// only legitimate interest is that the token is not usable afterwards.
func (a *API) handleRevoke(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return writeOAuthError(http.StatusBadRequest,
			&oauth.Error{Code: oauth.ErrInvalidRequest, Description: "malformed form body"})
	}
	w.Header().Set("Cache-Control", "no-store")
	raw := r.PostForm.Get("token")
	if raw == "" {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	if st, err := a.store(); err == nil {
		hash := auth.HashAPIToken(raw)
		if tok, err := st.GetOAuthTokenByHash(r.Context(), hash); err == nil {
			// Revoking either half takes the whole grant with it: a client
			// disconnecting means the pair, not one of them.
			_ = st.RevokeOAuthTokensForGrant(r.Context(), tok.GrantID)
		}
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func invalidGrant(desc string) error {
	return writeOAuthError(http.StatusBadRequest,
		&oauth.Error{Code: oauth.ErrInvalidGrant, Description: desc})
}
