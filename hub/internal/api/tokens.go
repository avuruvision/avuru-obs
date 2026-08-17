package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type createAPITokenRequest struct {
	Name string `json:"name"`
	// ExpiresInDays: zero or absent means no expiry (design/
	// 2026-08-13-api-tokens.md, "API"). Narrowing a token's OWN lifetime is a
	// property of the token, not of what its owner may do — unrelated to the
	// non-goal of narrowing a token below its owner's grants.
	ExpiresInDays int `json:"expiresInDays"`
}

// createAPITokenResponse is the ONE time the raw token is returned. The
// owner copies it immediately; only its hash is stored, so it can never be
// recovered.
type createAPITokenResponse struct {
	Token     string     `json:"token"` // raw — shown exactly once
	TokenHash string     `json:"tokenHash"`
	Prefix    string     `json:"prefix"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"` // nil = never expires
}

// apiTokenDTO is one token in the list — metadata only, never the raw
// secret. TokenHash is the delete handle (a preimage-resistant hash, not a
// secret; the prefix is not unique so it cannot serve as the identifier) —
// same reasoning as ingestKeyDTO.KeyHash. LastUsedAt/ExpiresAt are nil for
// "never used" / "never expires" rather than a zero-epoch timestamp, so a
// caller can't mistake either for a real date.
type apiTokenDTO struct {
	TokenHash  string     `json:"tokenHash"`
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

type apiTokensResponse struct {
	Tokens []apiTokenDTO `json:"tokens"`
}

func toAPITokenDTO(t storage.AuthToken) apiTokenDTO {
	dto := apiTokenDTO{
		TokenHash: t.TokenHash,
		Prefix:    t.Prefix,
		Name:      t.Name,
		CreatedAt: t.CreatedAt,
	}
	if !t.LastUsedAt.IsZero() {
		lu := t.LastUsedAt
		dto.LastUsedAt = &lu
	}
	if !t.ExpiresAt.IsZero() {
		ea := t.ExpiresAt
		dto.ExpiresAt = &ea
	}
	return dto
}

// handleListAPITokens returns the CALLER's own tokens, metadata only. A
// global admin may widen the scope to another user's with ?user=<id> — that
// widening happens HERE, via id.IsAdmin(), rather than as a second route:
// one URL, one resource. A non-admin passing ?user= gets 403 for asking for
// a capability they don't have (contrast with the revoke handler below,
// where the equivalent situation is a 404 — there the caller is claiming to
// own a specific hash, and confirming "that one exists, just not yours"
// is itself a leak; here they're just asking for a scope they don't hold).
func (a *API) handleListAPITokens(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	id := identityFrom(r.Context())
	if id == nil { // unreachable while the route requires auth; defensive
		return unauthorized()
	}
	userID := id.UserID
	if q := r.URL.Query().Get("user"); q != "" {
		if !id.IsAdmin() {
			return forbidden("requires global admin to list another user's tokens")
		}
		userID = q
	}
	tokens, err := st.ListAuthTokens(r.Context(), userID)
	if err != nil {
		return err
	}
	out := make([]apiTokenDTO, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toAPITokenDTO(t))
	}
	writeJSON(w, http.StatusOK, apiTokensResponse{Tokens: out})
	return nil
}

// handleCreateAPIToken mints a token for the CALLER — there is no way to
// mint one on another user's behalf through this route — and returns the
// raw secret exactly once (only its hash is persisted). No grants column:
// resolution reads the owner's live, so nothing here can drift from what
// IdentityFromAPIToken later reads back.
func (a *API) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	id := identityFrom(r.Context())
	if id == nil { // unreachable while the route requires auth; defensive
		return unauthorized()
	}
	var req createAPITokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return badRequest("name is required")
	}
	// Reuses the ingest-key cap rather than inventing a second one — same
	// column shape, same UI vocabulary.
	if len(name) > maxIngestKeyNameLen {
		return badRequest("name must be %d characters or fewer", maxIngestKeyNameLen)
	}
	if req.ExpiresInDays < 0 {
		return badRequest("expiresInDays must be zero or positive")
	}

	raw, prefix, hash := auth.NewAPIToken()
	tok := storage.AuthToken{
		TokenHash: hash,
		UserID:    id.UserID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: time.Now(),
	}
	if req.ExpiresInDays > 0 {
		tok.ExpiresAt = tok.CreatedAt.AddDate(0, 0, req.ExpiresInDays)
	}
	if err := st.CreateAuthToken(r.Context(), tok); err != nil {
		return err
	}
	resp := createAPITokenResponse{
		Token:     raw,
		TokenHash: hash,
		Prefix:    prefix,
		Name:      name,
		CreatedAt: tok.CreatedAt,
	}
	if !tok.ExpiresAt.IsZero() {
		ea := tok.ExpiresAt
		resp.ExpiresAt = &ea
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

// handleRevokeAPIToken tombstones a token by hash, scoped to its owner —
// UNLESS the caller is a global admin, who may revoke anyone's. Another
// user's hash comes back as ErrNotFound (404), never 403: a 403 would
// confirm the hash exists and simply belongs to someone else, which is
// exactly the information a preimage-resistant hash is supposed to
// withhold from a caller who does not hold it.
func (a *API) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	id := identityFrom(r.Context())
	if id == nil { // unreachable while the route requires auth; defensive
		return unauthorized()
	}
	hash := r.PathValue("hash")
	owner := id.UserID
	if id.IsAdmin() {
		// RevokeAuthToken is scoped by owner, so an admin acting on someone
		// else's token needs that owner resolved first rather than widening
		// the store method's contract for one caller. GetAuthTokenByHash's
		// ErrNotFound for an unknown hash maps to 404 here exactly as it
		// would for a non-admin — same outcome, same reasoning.
		tok, err := st.GetAuthTokenByHash(r.Context(), hash)
		if err != nil {
			return err
		}
		owner = tok.UserID
	}
	if err := st.RevokeAuthToken(r.Context(), owner, hash); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
