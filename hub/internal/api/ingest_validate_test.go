package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// internalMux registers the API with an ingest internal token and seeds one
// live key for "payments"; it returns the mux and the seeded RAW key.
func internalMux(t *testing.T, token string) (*http.ServeMux, string) {
	t.Helper()
	f := &storagetest.Fake{}
	raw, prefix, hash := auth.NewIngestKey()
	if err := f.CreateIngestKey(context.Background(), storage.AuthIngestKey{
		KeyHash: hash, Project: "payments", Name: "seed", Prefix: prefix, CreatedBy: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{IngestInternalToken: token})
	return mux, raw
}

func doAuthed(mux *http.ServeMux, method, path, body, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestValidateIngestKey(t *testing.T) {
	mux, seededRaw := internalMux(t, "sekret")

	// Wrong internal token → 401 regardless of key.
	if w := doAuthed(mux, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"`+seededRaw+`"}`, "Bearer nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad internal token status = %d, want 401", w.Code)
	}

	// Missing token → 401.
	if w := doAuthed(mux, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"`+seededRaw+`"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", w.Code)
	}

	// Valid internal token + valid key → {valid:true, project:"payments"}.
	w := doAuthed(mux, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"`+seededRaw+`"}`, "Bearer sekret")
	if w.Code != http.StatusOK {
		t.Fatalf("valid status = %d", w.Code)
	}
	var resp validateIngestKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Valid || resp.Project != "payments" {
		t.Fatalf("resp = %+v", resp)
	}

	// Unknown key → {valid:false}, still HTTP 200 (gateway caches negatives).
	w = doAuthed(mux, "POST", "/internal/v1/ingest-keys/validate",
		`{"key":"avuruk_bogus"}`, "Bearer sekret")
	if w.Code != http.StatusOK {
		t.Fatalf("unknown-key status = %d, want 200", w.Code)
	}
	resp = validateIngestKeyResponse{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Valid {
		t.Fatalf("bogus key validated: %+v", resp)
	}
}

// TestValidateIngestKeyNotRegisteredWithoutToken proves the endpoint is absent
// (404) when no internal token is configured — the safe default.
func TestValidateIngestKeyNotRegisteredWithoutToken(t *testing.T) {
	f := &storagetest.Fake{}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return f }, Config{})
	if w := doAuthed(mux, "POST", "/internal/v1/ingest-keys/validate", `{"key":"x"}`, "Bearer x"); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not registered)", w.Code)
	}
}
