package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// callTool runs one tool through the full JSON-RPC path and returns the
// decoded payload plus whether the result was flagged as a tool error. Going
// through Handle rather than calling run() directly is the point: it is the
// path a client takes, audit line included.
func callTool(t *testing.T, s *Server, name, args string) (map[string]any, bool) {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	got := call(t, s, body)
	if got["error"] != nil {
		t.Fatalf("%s: protocol error %v", name, got["error"])
	}
	res, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result in %v", name, got)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("%s: no content in %v", name, res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("%s: payload is not JSON: %s", name, text)
	}
	isErr, _ := res["isError"].(bool)
	return payload, isErr
}

func TestListServices(t *testing.T) {
	f := fakeWithServices("frontend", "payment-api")
	f.Services[0] = storage.ServiceStats{Name: "frontend", SpanCount: 3600, ErrorCount: 0}
	f.Services[1] = storage.ServiceStats{Name: "payment-api", SpanCount: 1800, ErrorCount: 180}
	s := serverWith(f)

	payload, isErr := callTool(t, s, "list_services", `{"window":"1h"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	services, _ := payload["services"].([]any)
	if len(services) != 2 {
		t.Fatalf("got %d services, want 2: %v", len(services), payload)
	}
	// Worst first: a truncated list has to be the part that matters.
	first, _ := services[0].(map[string]any)
	if first["service"] != "payment-api" {
		t.Errorf("first row = %v, want payment-api (the one with errors)", first["service"])
	}
	if first["errorRate"] != 0.1 {
		t.Errorf("errorRate = %v, want 0.1", first["errorRate"])
	}
	if first["ratePerSec"] != 0.5 {
		t.Errorf("ratePerSec = %v, want 0.5 (1800 spans over an hour)", first["ratePerSec"])
	}
	if payload["truncated"] != false {
		t.Errorf("truncated = %v, want false", payload["truncated"])
	}
}

func TestListServicesUnhealthyOnly(t *testing.T) {
	f := fakeWithServices("frontend", "payment-api")
	f.Services[0] = storage.ServiceStats{Name: "frontend", SpanCount: 100}
	f.Services[1] = storage.ServiceStats{Name: "payment-api", SpanCount: 100, ErrorCount: 5}
	payload, _ := callTool(t, serverWith(f), "list_services", `{"unhealthy_only":true}`)
	services, _ := payload["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("got %d services, want only the failing one: %v", len(services), payload)
	}
}

// A bounded response has to SAY it is bounded. A model handed a top-N with no
// marker reasons about it as the whole estate.
func TestListServicesTruncationIsAnnounced(t *testing.T) {
	f := &storagetest.Fake{}
	for i := 0; i < 30; i++ {
		f.Services = append(f.Services, storage.ServiceStats{Name: fmt.Sprintf("svc-%02d", i), SpanCount: uint64(i + 1)})
	}
	payload, _ := callTool(t, serverWith(f), "list_services", `{"limit":10}`)
	services, _ := payload["services"].([]any)
	if len(services) != 10 {
		t.Fatalf("got %d services, want 10", len(services))
	}
	if payload["truncated"] != true {
		t.Errorf("truncated = %v, want true", payload["truncated"])
	}
	if payload["total"] != float64(30) {
		t.Errorf("total = %v, want 30 — the count is how a model knows what it is missing", payload["total"])
	}
}

// An argument the model invented must be refused, not ignored. Silence gets
// read as "the filter was applied", and every number after that is wrong.
func TestUnknownArgumentIsRefused(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "list_services", `{"servicee":"a"}`)
	if !isErr {
		t.Fatalf("an unknown argument was accepted: %v", payload)
	}
	if payload["error"] == nil {
		t.Errorf("tool error carried no message: %v", payload)
	}
}

func TestToolsListNamesTheTools(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	res, _ := got["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list is empty: %v", res)
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] == nil || first["description"] == nil || first["inputSchema"] == nil {
		t.Errorf("a tool definition is missing name/description/inputSchema: %v", first)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"drop_database"}}`)
	e, ok := got["error"].(map[string]any)
	if !ok || e["code"] != float64(codeInvalidParams) {
		t.Errorf("unknown tool = %v, want an invalid-params error", got)
	}
}
