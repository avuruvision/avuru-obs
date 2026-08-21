package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const maxIngestKeyNameLen = 200

type createIngestKeyRequest struct {
	Name string `json:"name"`
}

// createIngestKeyResponse is the ONE time the raw key is returned. The admin
// copies it immediately; only its hash is stored, so it can never be recovered.
type createIngestKeyResponse struct {
	Key       string    `json:"key"` // raw — shown exactly once
	KeyHash   string    `json:"keyHash"`
	Prefix    string    `json:"prefix"`
	Name      string    `json:"name"`
	Project   string    `json:"project"`
	CreatedAt time.Time `json:"createdAt"`
}

// ingestKeyDTO is one key in the list — metadata only, never the raw secret.
// KeyHash is the stable delete handle (a preimage-resistant hash, not a secret;
// the prefix is not unique so it cannot serve as the identifier).
type ingestKeyDTO struct {
	KeyHash   string    `json:"keyHash"`
	Prefix    string    `json:"prefix"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type ingestKeysResponse struct {
	Keys []ingestKeyDTO `json:"keys"`
}

func toIngestKeyDTO(k storage.AuthIngestKey) ingestKeyDTO {
	return ingestKeyDTO{
		KeyHash:   k.KeyHash,
		Prefix:    k.Prefix,
		Name:      k.Name,
		CreatedBy: k.CreatedBy,
		CreatedAt: k.CreatedAt,
	}
}

// handleListIngestKeys returns the live ingest keys for a project (prefix +
// metadata only). Keys can be scoped to any project id — default, config, or
// db — so it validates the slug shape but never requires a db project row.
func (a *API) handleListIngestKeys(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	project := r.PathValue("id")
	if !projectIDRe.MatchString(project) {
		return badRequest("invalid project id")
	}
	keys, err := st.ListIngestKeys(r.Context(), project)
	if err != nil {
		return err
	}
	out := make([]ingestKeyDTO, 0, len(keys))
	for _, k := range keys {
		out = append(out, toIngestKeyDTO(k))
	}
	writeJSON(w, http.StatusOK, ingestKeysResponse{Keys: out})
	return nil
}

// handleCreateIngestKey mints a key for the project and returns the raw secret
// exactly once (only its hash is persisted).
func (a *API) handleCreateIngestKey(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	project := r.PathValue("id")
	if !projectIDRe.MatchString(project) {
		return badRequest("invalid project id")
	}
	var req createIngestKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return badRequest("name is required")
	}
	if len(name) > maxIngestKeyNameLen {
		return badRequest("name must be %d characters or fewer", maxIngestKeyNameLen)
	}

	// A key stamps its project onto every payload it authenticates. Stamping an
	// aggregate would write rows no member can read — the union reads members,
	// never the aggregate id itself.
	agg, err := a.isAggregate(r.Context(), project)
	if err != nil {
		return err
	}
	if agg {
		return aggregateWriteConflict(project, "create the key on the member project whose telemetry it will authenticate")
	}

	raw, prefix, hash := auth.NewIngestKey()
	k := storage.AuthIngestKey{
		KeyHash:   hash,
		Project:   project,
		Name:      name,
		Prefix:    prefix,
		CreatedBy: identityUserID(r.Context()),
		CreatedAt: time.Now(),
	}
	if err := st.CreateIngestKey(r.Context(), k); err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, createIngestKeyResponse{
		Key:       raw,
		KeyHash:   hash,
		Prefix:    prefix,
		Name:      name,
		Project:   project,
		CreatedAt: k.CreatedAt,
	})
	return nil
}

// handleRevokeIngestKey tombstones a key by hash within its project. The
// project scope means one project's admin URL cannot revoke another's key by
// hash guess. ErrNotFound (unknown or already-revoked) maps to 404.
func (a *API) handleRevokeIngestKey(w http.ResponseWriter, r *http.Request) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	if err := st.RevokeIngestKey(r.Context(), r.PathValue("id"), r.PathValue("hash")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
