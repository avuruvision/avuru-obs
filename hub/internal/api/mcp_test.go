package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

const mcpInitBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

func mcpPost(mux *http.ServeMux, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// The module gate: an install that did not opt in has no route at all, rather
// than a route that refuses. 404 is the honest answer — there is nothing here.
func TestMCPAbsentWithoutTheModule(t *testing.T) {
	set, err := modules.Parse("core,logs")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return &storagetest.Fake{} }, Config{Modules: set})
	if w := mcpPost(mux, mcpInitBody, nil); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", w.Code, w.Body)
	}
}

func TestMCPRefusesAnUnauthenticatedCall(t *testing.T) {
	mux, _, _, _ := bearerMux(t, Config{})
	if w := mcpPost(mux, mcpInitBody, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (%s)", w.Code, w.Body)
	}
}

// A personal API token is a first-class credential here: it authenticates as
// its owner and reads that owner's projects. That is the entire authorization
// story for this client — there is no second one.
func TestMCPAcceptsABearerTokenAndKeepsItsScope(t *testing.T) {
	mux, _, _, raw := bearerMux(t, Config{})
	hdr := bearer(raw)
	hdr["X-Avuru-Tenant"] = "prod"

	w := mcpPost(mux, mcpInitBody, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body)
	}
	var got struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Result.ProtocolVersion == "" || got.Result.ServerInfo.Name == "" {
		t.Errorf("initialize returned an empty handshake: %+v", got.Result)
	}

	// The same token outside its owner's grants is refused here exactly as it
	// is on /api/v1/services: the tenancy rule is one rule, not one per client.
	hdr["X-Avuru-Tenant"] = "payments"
	if w := mcpPost(mux, mcpInitBody, hdr); w.Code != http.StatusForbidden {
		t.Fatalf("token outside its owner's grants: %d, want 403 (%s)", w.Code, w.Body)
	}
}

// A notification is delivered, not answered — 202 with no body, which is how a
// client tells "you got it" from "here is your reply".
func TestMCPNotificationIs202(t *testing.T) {
	mux, _, _, raw := bearerMux(t, Config{})
	hdr := bearer(raw)
	hdr["X-Avuru-Tenant"] = "prod"
	w := mcpPost(mux, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, hdr)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", w.Code, w.Body)
	}
	if w.Body.Len() != 0 {
		t.Errorf("notification answered with a body: %s", w.Body)
	}
}
