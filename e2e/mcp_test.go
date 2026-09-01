//go:build e2e

// The MCP server against a real stack: JSON-RPC in, the seeded fixture out.
// The compose hub sets no AVURUOBS_MODULES, so every module including mcp is
// active and POST /mcp exists — the same reason green and ai are exercised here.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mcpCall sends one JSON-RPC request as the authenticated admin and returns
// the decoded reply.
func mcpCall(method, params string) (map[string]any, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		body += `,"params":` + params
	}
	body += "}"
	resp, err := apiClient.Post(hubURL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /mcp %s: %s", method, resp.Status)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if e, ok := out["error"]; ok {
		return nil, fmt.Errorf("%s: %v", method, e)
	}
	return out, nil
}

// mcpTool runs one tool and returns its payload plus whether the tool itself
// reported an error (as opposed to the protocol doing so).
func mcpTool(name, args string) (map[string]any, bool, error) {
	out, err := mcpCall("tools/call", `{"name":"`+name+`","arguments":`+args+`}`)
	if err != nil {
		return nil, false, err
	}
	res, _ := out["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return nil, false, fmt.Errorf("%s: no content in %v", name, res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, false, fmt.Errorf("%s: payload is not JSON: %s", name, text)
	}
	isErr, _ := res["isError"].(bool)
	return payload, isErr, nil
}

func TestMCPHandshakeAndTools(t *testing.T) {
	out, err := mcpCall("initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}`)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	res, _ := out["result"].(map[string]any)
	if res["protocolVersion"] == nil {
		t.Fatalf("no protocolVersion in %v", res)
	}

	out, err = mcpCall("tools/list", "")
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	res, _ = out["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		def, _ := tl.(map[string]any)
		names[fmt.Sprint(def["name"])] = true
	}
	// Every module runs in the compose stack, so all six must be listed.
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace", "search_logs", "list_error_issues"} {
		if !names[want] {
			t.Errorf("tools/list is missing %s: %v", want, names)
		}
	}
}

// The seeded fixture is one trace in seed-checkout: three spans, one of them
// the entry span, and it failed. So the service reports exactly one request at
// a 100% error rate — deterministic, unlike the HotROD traffic beside it.
func TestMCPServiceContextReadsTheSeededService(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("service_context", `{"service":"seed-checkout","window":"2h"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		red, _ := payload["red"].(map[string]any)
		if red["spanCount"] != float64(1) {
			return fmt.Errorf("spanCount = %v, want the fixture's 1 entry span", red["spanCount"])
		}
		if red["errorRate"] != float64(1) {
			return fmt.Errorf("errorRate = %v, want 1 — the seeded entry span failed", red["errorRate"])
		}
		return nil
	})
}

func TestMCPGetTraceRollsUpSelfTime(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("get_trace", `{"trace_id":"`+seededTraceID+`"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		spans, _ := payload["spans"].([]any)
		if len(spans) != 3 {
			return fmt.Errorf("got %d spans, want the fixture's 3", len(spans))
		}
		services, _ := payload["services"].([]any)
		if len(services) != 1 {
			return fmt.Errorf("got %d services in the rollup, want 1 (every span is seed-checkout)", len(services))
		}
		row, _ := services[0].(map[string]any)
		if row["service"] != "seed-checkout" {
			return fmt.Errorf("rollup service = %v", row["service"])
		}
		// Every span in this fixture is seed-checkout, so the service's self
		// time must come to the root's full 250ms: what the root subtracts for
		// waiting on its children is exactly what those children add back.
		// Anything else means time is being double-counted or lost, which is
		// the failure mode a self-time rollup exists to avoid.
		self, _ := row["selfTimeMs"].(float64)
		if self != 250 {
			return fmt.Errorf("selfTimeMs = %v, want the root's whole 250ms (one service, nothing waited on)", self)
		}
		if row["errorCount"] != float64(2) {
			return fmt.Errorf("errorCount = %v, want the fixture's 2 error spans", row["errorCount"])
		}
		return nil
	})
}

// The rule this server turns on, proven against real data: a misspelled name
// is an error naming the near matches, never an empty result a model would
// read as "this service is dead".
func TestMCPUnknownServiceNamesNearMatches(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("service_context", `{"service":"seed-checkut","window":"2h"}`)
		if err != nil {
			return err
		}
		if !isErr {
			return fmt.Errorf("a misspelled service was accepted: %v", payload)
		}
		hints, _ := payload["didYouMean"].([]any)
		for _, h := range hints {
			if h == "seed-checkout" {
				return nil
			}
		}
		return fmt.Errorf("didYouMean = %v, want seed-checkout", hints)
	})
}

// Correlated logs through the tool, on the same trace the REST suite reads:
// the seeded fixture puts 2 log records on it.
func TestMCPSearchLogsByTraceID(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("search_logs", `{"trace_id":"`+seededTraceID+`"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		logs, _ := payload["logs"].([]any)
		if len(logs) != 2 {
			return fmt.Errorf("got %d logs, want the fixture's 2", len(logs))
		}
		return nil
	})
}
