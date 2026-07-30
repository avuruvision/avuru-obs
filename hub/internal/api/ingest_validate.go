package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type validateIngestKeyRequest struct {
	Key string `json:"key"`
}

// validateIngestKeyResponse is the gateway-facing verdict. Both positive and
// negative verdicts return HTTP 200 — the gateway caches negatives too, so an
// unknown key is a normal answer, not an error.
type validateIngestKeyResponse struct {
	Valid   bool   `json:"valid"`
	Project string `json:"project,omitempty"`
}

// handleValidateIngestKey answers the gateway's control-plane validation call.
// It authenticates the CALLER (the gateway) with the shared internal token, not
// the ingest key — the key is what's being validated. The hub is never in the
// telemetry byte-path; this is the only hub↔gateway coupling for enforcement.
func (a *API) handleValidateIngestKey(w http.ResponseWriter, r *http.Request) error {
	if !a.validInternalToken(r) {
		return unauthorized()
	}
	st, err := a.store()
	if err != nil {
		return err
	}
	var req validateIngestKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		return decodeJSONError(err)
	}
	if req.Key == "" {
		writeJSON(w, http.StatusOK, validateIngestKeyResponse{Valid: false})
		return nil
	}
	k, err := st.GetIngestKeyByHash(r.Context(), auth.HashIngestKey(req.Key))
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, validateIngestKeyResponse{Valid: false})
		return nil
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, validateIngestKeyResponse{Valid: true, Project: k.Project})
	return nil
}

// validInternalToken constant-time compares the Bearer token against the
// configured internal token. An unset token (empty) never matches — but the
// route is not even registered then (see Register), so this is defence in depth.
func (a *API) validInternalToken(r *http.Request) bool {
	if a.cfg.IngestInternalToken == "" {
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.IngestInternalToken)) == 1
}
