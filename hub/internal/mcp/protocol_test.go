package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// call runs one request body through a server and returns the decoded reply.
// A notification (no reply) returns nil.
func call(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	out, err := s.Handle(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Handle(%s): %v", body, err)
	}
	if out == nil {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding reply %s: %v", out, err)
	}
	return got
}

func testServer() *Server {
	return &Server{Modules: modules.AllSet(), Tenant: "default", Tenants: []string{"default"}, Version: "test"}
}

func TestInitialize(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", got)
	}
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", res["protocolVersion"], ProtocolVersion)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Errorf("server must advertise the tools capability: %v", res)
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok || info["name"] != ServerName || info["version"] != "test" {
		t.Errorf("serverInfo = %v, want name=%q version=test", info, ServerName)
	}
}

// A notification carries no id and takes no reply at all. Answering one is a
// protocol violation some clients treat as a fatal desync, so this is not a
// cosmetic assertion.
func TestNotificationGetsNoReply(t *testing.T) {
	if got := call(t, testServer(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`); got != nil {
		t.Errorf("notification answered with %v, want no reply", got)
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		code float64 // JSON numbers decode as float64
	}{
		{"unparseable body", `{not json`, codeParse},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, codeInvalidRequest},
		{"batch is not supported", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, codeMethodNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call(t, testServer(), tt.body)
			e, ok := got["error"].(map[string]any)
			if !ok {
				t.Fatalf("no error object in %v", got)
			}
			if e["code"] != tt.code {
				t.Errorf("code = %v, want %v", e["code"], tt.code)
			}
			if got["result"] != nil {
				t.Errorf("error reply also carried a result: %v", got)
			}
		})
	}
}

func TestPing(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":"a","method":"ping"}`)
	if got["error"] != nil {
		t.Fatalf("ping errored: %v", got)
	}
	if got["id"] != "a" {
		t.Errorf("id = %v, want the string \"a\" echoed back unchanged", got["id"])
	}
}
