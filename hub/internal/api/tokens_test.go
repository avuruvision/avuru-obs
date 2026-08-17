package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// tokensMux seeds three signed-in users: owner and other, DELIBERATELY given
// no grants at all (the "logged out of everything" case the design calls
// out — authenticated(), not secured(), is what lets them reach these routes
// regardless), plus the bootstrap global admin. Two non-admin users is what
// lets the cross-user tests below tell "not found" from "found but not
// yours" the way the design requires.
func tokensMux(t *testing.T) (mux *http.ServeMux, f *storagetest.Fake, ownerCookie, otherCookie, adminCookie *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	f = &storagetest.Fake{}
	svc := auth.NewService(func() storage.Store { return f }, time.Hour)
	if _, err := svc.Bootstrap(ctx, "root-pw"); err != nil {
		t.Fatal(err)
	}
	h, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "owner", Email: "owner@x.io", Name: "Owner", PasswordHash: h, Origin: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAuthUser(ctx, storage.AuthUser{
		ID: "other", Email: "other@x.io", Name: "Other", PasswordHash: h, Origin: "local"}); err != nil {
		t.Fatal(err)
	}

	adminToken, _, err := svc.Login(ctx, "admin", "root-pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, _, err := svc.Login(ctx, "owner@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := svc.Login(ctx, "other@x.io", "pw", "test")
	if err != nil {
		t.Fatal(err)
	}

	mux = http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{Auth: svc})
	return mux, f,
		&http.Cookie{Name: sessionCookieName, Value: ownerToken},
		&http.Cookie{Name: sessionCookieName, Value: otherToken},
		&http.Cookie{Name: sessionCookieName, Value: adminToken}
}

// TestCreateAPITokenReturnsSecretOnce: the raw token is in the create
// response and nowhere else — the list that follows must show the prefix
// but never the raw secret.
func TestCreateAPITokenReturnsSecretOnce(t *testing.T) {
	mux, _, owner, _, _ := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"ci-script"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Token, TokenHash, Prefix, Name string
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "avurut_") {
		t.Fatalf("no raw token in create response: %s", resp.Token)
	}
	if resp.Prefix != resp.Token[:12] || resp.Name != "ci-script" || resp.TokenHash == "" {
		t.Fatalf("resp = %+v", resp)
	}

	w = doBody(mux, "GET", "/api/v1/tokens", owner, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, resp.Token) {
		t.Fatalf("list leaked the raw token: %s", body)
	}
	if !strings.Contains(body, resp.Prefix) {
		t.Fatalf("list missing the prefix: %s", body)
	}
}

// TestCreateAPITokenValidation pins name requirements: required, and capped
// by the SAME constant ingest keys use — not a second one.
func TestCreateAPITokenValidation(t *testing.T) {
	mux, _, owner, _, _ := tokensMux(t)

	if w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"  "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", w.Code)
	}
	tooLong := `{"name":"` + strings.Repeat("a", maxIngestKeyNameLen+1) + `"}`
	if w := doBody(mux, "POST", "/api/v1/tokens", owner, tooLong); w.Code != http.StatusBadRequest {
		t.Fatalf("name over the ingest-key cap: %d, want 400", w.Code)
	}
	// The cap itself is fine.
	atCap := `{"name":"` + strings.Repeat("a", maxIngestKeyNameLen) + `"}`
	if w := doBody(mux, "POST", "/api/v1/tokens", owner, atCap); w.Code != http.StatusCreated {
		t.Fatalf("name at the ingest-key cap: %d, want 201, body=%s", w.Code, w.Body)
	}
}

// TestCreateAPITokenExpiry: expiresInDays of 0 or absent means no expiry;
// a positive value sets one.
func TestCreateAPITokenExpiry(t *testing.T) {
	mux, _, owner, _, _ := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"no-expiry"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var noExpiry struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(w.Body).Decode(&noExpiry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if noExpiry.ExpiresAt != nil {
		t.Fatalf("expiresInDays absent got an expiry: %v", noExpiry.ExpiresAt)
	}

	w = doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"expires-soon","expiresInDays":7}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var withExpiry struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(w.Body).Decode(&withExpiry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if withExpiry.ExpiresAt == nil {
		t.Fatal("expiresInDays=7 produced no expiry")
	}
	if until := time.Until(*withExpiry.ExpiresAt); until < 6*24*time.Hour || until > 8*24*time.Hour {
		t.Fatalf("expiresAt = %v, want ~7 days out", withExpiry.ExpiresAt)
	}
}

// TestListAPITokensOnlyCallersOwn: GET returns exactly the caller's tokens,
// never another user's — metadata only.
func TestListAPITokensOnlyCallersOwn(t *testing.T) {
	mux, _, owner, other, _ := tokensMux(t)

	doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"owner-1"}`)
	doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"owner-2"}`)
	doBody(mux, "POST", "/api/v1/tokens", other, `{"name":"other-1"}`)

	w := doBody(mux, "GET", "/api/v1/tokens", owner, "")
	var resp struct {
		Tokens []struct{ Name string } `json:"tokens"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 2 {
		t.Fatalf("owner's tokens = %+v, want exactly 2", resp.Tokens)
	}
	for _, tok := range resp.Tokens {
		if tok.Name == "other-1" {
			t.Fatalf("owner's list leaked other's token: %+v", resp.Tokens)
		}
	}

	w = doBody(mux, "GET", "/api/v1/tokens", other, "")
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 1 || resp.Tokens[0].Name != "other-1" {
		t.Fatalf("other's tokens = %+v, want exactly [other-1]", resp.Tokens)
	}
}

// TestRevokeOwnAPIToken: the owner revokes their own token; a second revoke
// of the same hash is 404 (already gone), matching the ingest-key precedent.
func TestRevokeOwnAPIToken(t *testing.T) {
	mux, _, owner, _, _ := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"k"}`)
	var created struct{ TokenHash string }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.TokenHash == "" {
		t.Fatal("no tokenHash in create response (needed as the revoke handle)")
	}

	w = doBody(mux, "DELETE", "/api/v1/tokens/"+created.TokenHash, owner, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", w.Code)
	}
	w = doBody(mux, "GET", "/api/v1/tokens", owner, "")
	if strings.Contains(w.Body.String(), created.TokenHash) {
		t.Fatalf("revoked token still listed: %s", w.Body)
	}
	w = doBody(mux, "DELETE", "/api/v1/tokens/"+created.TokenHash, owner, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("double revoke status = %d, want 404", w.Code)
	}
}

// TestRevokeAnotherUsersTokenIs404NotForbidden is the security-shaped
// assertion the plan calls out explicitly: a 403 would confirm the hash
// exists (just not yours), which is exactly the thing a preimage-resistant
// hash is supposed to withhold. It must read identically to an unknown hash.
func TestRevokeAnotherUsersTokenIs404NotForbidden(t *testing.T) {
	mux, f, owner, other, _ := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"owner-secret"}`)
	var created struct{ TokenHash string }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	w = doBody(mux, "DELETE", "/api/v1/tokens/"+created.TokenHash, other, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("other user revoking owner's hash: %d, want 404 (NOT 403)", w.Code)
	}
	// And it must not actually have been revoked.
	if _, ok := f.AuthTokens[created.TokenHash]; !ok {
		t.Fatal("owner's token was revoked by another user's failed attempt")
	}

	// An unknown hash gets the identical 404 — the whole point.
	w2 := doBody(mux, "DELETE", "/api/v1/tokens/deadbeefdeadbeef", other, "")
	if w2.Code != http.StatusNotFound {
		t.Fatalf("unknown hash: %d, want 404", w2.Code)
	}
}

// TestListTokensUserParamAdminOnly: ?user= widens the list to another user's
// tokens, but only for a global admin — a non-admin passing it gets 403 (this
// one IS a 403: the caller isn't claiming to own a specific hash, they're
// asking for a capability they don't have).
func TestListTokensUserParamAdminOnly(t *testing.T) {
	mux, _, owner, other, admin := tokensMux(t)

	doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"owner-1"}`)

	w := doBody(mux, "GET", "/api/v1/tokens?user=owner", other, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin ?user=: %d, want 403", w.Code)
	}

	w = doBody(mux, "GET", "/api/v1/tokens?user=owner", admin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin ?user=: %d, want 200, body=%s", w.Code, w.Body)
	}
	var resp struct {
		Tokens []struct{ Name string } `json:"tokens"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 1 || resp.Tokens[0].Name != "owner-1" {
		t.Fatalf("admin's view of owner's tokens = %+v, want exactly [owner-1]", resp.Tokens)
	}
}

// TestAdminCanDeleteAnyonesToken: a global admin revokes a token that
// belongs to someone else entirely.
func TestAdminCanDeleteAnyonesToken(t *testing.T) {
	mux, _, owner, _, admin := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"owner-1"}`)
	var created struct{ TokenHash string }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	w = doBody(mux, "DELETE", "/api/v1/tokens/"+created.TokenHash, admin, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("admin revoking owner's token: %d, want 204, body=%s", w.Code, w.Body)
	}
	w = doBody(mux, "GET", "/api/v1/tokens", owner, "")
	if strings.Contains(w.Body.String(), created.TokenHash) {
		t.Fatalf("admin-revoked token still listed for its owner: %s", w.Body)
	}
}

// TestZeroGrantUserCanListAndRevokeOwnTokens pins the load-bearing choice of
// authenticated() over secured(): tokensMux's owner and other are seeded with
// NO grants at all, and every test above already exercises them successfully
// — this test makes that property explicit and would fail loudly (403) if
// these routes were ever changed to secured().
func TestZeroGrantUserCanListAndRevokeOwnTokens(t *testing.T) {
	mux, _, owner, _, _ := tokensMux(t)

	w := doBody(mux, "POST", "/api/v1/tokens", owner, `{"name":"k"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("zero-grant user creating a token: %d, want 201, body=%s", w.Code, w.Body)
	}
	var created struct{ TokenHash string }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if w := doBody(mux, "GET", "/api/v1/tokens", owner, ""); w.Code != http.StatusOK {
		t.Fatalf("zero-grant user listing own tokens: %d, want 200", w.Code)
	}
	if w := doBody(mux, "DELETE", "/api/v1/tokens/"+created.TokenHash, owner, ""); w.Code != http.StatusNoContent {
		t.Fatalf("zero-grant user revoking own token: %d, want 204", w.Code)
	}
}

// TestTokensRouteRequiresAuthentication: no session at all is 401, same as
// every other authenticated() route.
func TestTokensRouteRequiresAuthentication(t *testing.T) {
	mux, _, _, _, _ := tokensMux(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/tokens"},
		{"POST", "/api/v1/tokens"},
		{"DELETE", "/api/v1/tokens/deadbeef"},
	} {
		w := doBody(mux, tc.method, tc.path, nil, "{}")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}
