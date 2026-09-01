package mcp

import (
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

func toolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	got := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	res, _ := got["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		def, _ := tl.(map[string]any)
		names[def["name"].(string)] = true
	}
	return names
}

// The capabilities rule, and the reason it matters more here than in the
// sidebar: a tool that EXISTS will be called. A model that calls one and gets
// a failure reads the failure as a fact about the estate — "this install has
// no logs" becomes "this service logs nothing" — and reasons on from there.
// So a disabled module's tool is absent, not present-and-always-failing.
func TestToolsListHidesDisabledModules(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(fakeWithServices("a"))
	s.Modules = core

	names := toolNames(t, s)
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace"} {
		if !names[want] {
			t.Errorf("%s must always be listed — it reads core trace data", want)
		}
	}
	for _, gone := range []string{"search_logs", "list_error_issues"} {
		if names[gone] {
			t.Errorf("%s listed with its module off", gone)
		}
	}
}

func TestToolsListShowsEverythingWhenEveryModuleRuns(t *testing.T) {
	names := toolNames(t, serverWith(fakeWithServices("a")))
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace", "search_logs", "list_error_issues"} {
		if !names[want] {
			t.Errorf("%s missing from tools/list", want)
		}
	}
	if len(names) != 6 {
		t.Errorf("tools/list has %d tools, want exactly 6: %v", len(names), names)
	}
}

// Calling a disabled module's tool by name anyway is invalid-params naming the
// module, not method-not-found: the tool exists, this install does not run it,
// and an agent that is told which module is off can report that instead of
// concluding the signal does not exist.
func TestCallingADisabledToolNamesTheModule(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(fakeWithServices("a"))
	s.Modules = core

	got := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_logs","arguments":{}}}`)
	e, ok := got["error"].(map[string]any)
	if !ok || e["code"] != float64(codeInvalidParams) {
		t.Fatalf("got %v, want an invalid-params error", got)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "logs") {
		t.Errorf("message %q does not name the missing module", msg)
	}
}
