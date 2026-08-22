package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestCreateIngestKeyReturnsSecretOnce(t *testing.T) {
	mux, c, _ := adminMux(t)

	w := doBody(mux, "POST", "/api/v1/projects/payments/keys", c, `{"name":"prod-exporter"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Key, KeyHash, Prefix, Project, Name string
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.Key, "avuruk_") {
		t.Fatalf("no raw key in create response: %s", resp.Key)
	}
	if resp.Prefix != resp.Key[:12] || resp.Project != "payments" || resp.Name != "prod-exporter" {
		t.Fatalf("resp = %+v", resp)
	}

	// List never returns the raw key — only prefix + metadata.
	w = doBody(mux, "GET", "/api/v1/projects/payments/keys", c, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, resp.Key) {
		t.Fatalf("list leaked the raw key: %s", body)
	}
	if !strings.Contains(body, resp.Prefix) {
		t.Fatalf("list missing the prefix: %s", body)
	}
}

func TestCreateIngestKeyValidation(t *testing.T) {
	mux, c, _ := adminMux(t)

	// Empty name is rejected.
	if w := doBody(mux, "POST", "/api/v1/projects/payments/keys", c, `{"name":"  "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", w.Code)
	}
	// Malformed project id is rejected.
	if w := doBody(mux, "POST", "/api/v1/projects/Not_A_Slug/keys", c, `{"name":"k"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad project status = %d, want 400", w.Code)
	}
}

func TestRevokeIngestKey(t *testing.T) {
	mux, c, _ := adminMux(t)

	w := doBody(mux, "POST", "/api/v1/projects/payments/keys", c, `{"name":"k"}`)
	var created struct{ KeyHash string }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.KeyHash == "" {
		t.Fatal("no keyHash in create response (needed as the revoke handle)")
	}

	// Revoke it.
	w = doBody(mux, "DELETE", "/api/v1/projects/payments/keys/"+created.KeyHash, c, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", w.Code)
	}
	// Gone from the list.
	w = doBody(mux, "GET", "/api/v1/projects/payments/keys", c, "")
	if strings.Contains(w.Body.String(), created.KeyHash) {
		t.Fatalf("revoked key still listed: %s", w.Body)
	}
	// Second revoke → 404.
	w = doBody(mux, "DELETE", "/api/v1/projects/payments/keys/"+created.KeyHash, c, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("double revoke status = %d, want 404", w.Code)
	}
}

func TestIngestKeyCRUDAdminOnly(t *testing.T) {
	mux, c := authedMux(t) // editor-on-payments, not a global admin
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/projects/payments/keys"},
		{"POST", "/api/v1/projects/payments/keys"},
		{"DELETE", "/api/v1/projects/payments/keys/deadbeef"},
	} {
		w := authDo(mux, tc.method, tc.path, c, map[string]string{"Content-Type": "application/json"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403 (admin-only)", tc.method, tc.path, w.Code)
		}
	}
}

// An ingest key stamps its project onto everything it authenticates. On an
// aggregate that would write rows under an id no member reads — the union
// reads members, never the aggregate itself — so the key is refused where it
// would be useless.
func TestCreateIngestKeyRefusedOnAggregate(t *testing.T) {
	mux, c, f := adminMux(t)
	f.Projects = map[string]storage.Project{
		"estate": {ID: "estate", Members: []string{"prod-eu"}},
	}

	w := doBody(mux, "POST", "/api/v1/projects/estate/keys", c, `{"name":"prod-exporter"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body)
	}
	if len(f.IngestKeys) != 0 {
		t.Fatalf("key created anyway: %+v", f.IngestKeys)
	}
}
