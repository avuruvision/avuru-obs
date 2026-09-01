package mcp

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

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

func TestSearchTraces(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.Page = storage.TracePage{Traces: []storage.TraceSummary{{
		TraceID: "abc", RootService: "payment-api", RootOperation: "POST /pay",
		StartTime: testNow.Add(-time.Minute), Duration: 250 * time.Millisecond,
		SpanCount: 3, ErrorCount: 1, StatusCode: "Error",
	}}}
	s := serverWith(f)

	payload, isErr := callTool(t, s, "search_traces", `{"service":"payment-api","status":"error","min_duration_ms":100,"window":"30m"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	traces, _ := payload["traces"].([]any)
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1: %v", len(traces), payload)
	}
	first, _ := traces[0].(map[string]any)
	if first["traceId"] != "abc" || first["durationMs"] != float64(250) {
		t.Errorf("trace row = %v", first)
	}
	// The filters must reach storage, not be dropped on the floor between the
	// tool schema and the query — that is the whole failure mode of a second
	// read path.
	if f.LastTraceQuery.Service != "payment-api" {
		t.Errorf("service filter = %q, want payment-api", f.LastTraceQuery.Service)
	}
	if f.LastTraceQuery.Status != "error" {
		t.Errorf("status filter = %q, want error", f.LastTraceQuery.Status)
	}
	if f.LastTraceQuery.MinDuration != 100*time.Millisecond {
		t.Errorf("min duration = %v, want 100ms", f.LastTraceQuery.MinDuration)
	}
	if !f.LastTraceQuery.ExcludeAux {
		t.Error("auxiliary traffic must be excluded — health checks are not what anyone is investigating")
	}
}

// A misspelled service is an error naming the near matches, on every tool that
// takes one — not just on service_context.
func TestSearchTracesUnknownService(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("payment-api")), "search_traces", `{"service":"paymnt-api"}`)
	if !isErr {
		t.Fatalf("unknown service accepted: %v", payload)
	}
	hints, _ := payload["didYouMean"].([]any)
	if len(hints) == 0 || hints[0] != "payment-api" {
		t.Errorf("didYouMean = %v, want payment-api", hints)
	}
}

func TestSearchTracesRejectsAnInventedStatus(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "search_traces", `{"status":"weird"}`)
	if !isErr {
		t.Fatalf("invented status accepted: %v", payload)
	}
}

// The store reports "there is more" through its cursor; the payload has to
// pass that on, because a model reading 20 of 900 traces as "all of them" will
// conclude the failure is rarer than it is.
func TestSearchTracesTruncationFollowsTheCursor(t *testing.T) {
	f := fakeWithServices("a")
	f.Page = storage.TracePage{
		Traces:     []storage.TraceSummary{{TraceID: "one", StartTime: testNow}},
		NextCursor: &storage.TraceCursor{TraceID: "one", Timestamp: testNow},
	}
	payload, _ := callTool(t, serverWith(f), "search_traces", `{}`)
	if payload["truncated"] != true {
		t.Errorf("truncated = %v, want true", payload["truncated"])
	}
}

func TestGetTrace(t *testing.T) {
	// A 250ms root in payment-api with two children in ledger: the root's own
	// work is 250 - (100 + 40) = 110ms, and ledger accounts for 140ms.
	f := fakeWithServices("payment-api", "ledger")
	f.Traces = map[string]storage.Trace{"abc": {TraceID: "abc", Spans: []storage.Span{
		{SpanID: "root", Service: "payment-api", Operation: "POST /pay", Kind: "Server",
			StartTime: testNow, Duration: 250 * time.Millisecond, StatusCode: "Error"},
		{SpanID: "a", ParentSpanID: "root", Service: "ledger", Operation: "SELECT",
			StartTime: testNow, Duration: 100 * time.Millisecond},
		{SpanID: "b", ParentSpanID: "root", Service: "ledger", Operation: "INSERT",
			StartTime: testNow.Add(time.Millisecond), Duration: 40 * time.Millisecond},
	}}}

	payload, isErr := callTool(t, serverWith(f), "get_trace", `{"trace_id":"abc"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	spans, _ := payload["spans"].([]any)
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3", len(spans))
	}
	services, _ := payload["services"].([]any)
	if len(services) != 2 {
		t.Fatalf("got %d services in the rollup, want 2: %v", len(services), payload["services"])
	}
	byName := map[string]map[string]any{}
	for _, sv := range services {
		row, _ := sv.(map[string]any)
		byName[row["service"].(string)] = row
	}
	if got := byName["ledger"]["selfTimeMs"]; got != float64(140) {
		t.Errorf("ledger self time = %v, want 140 (100 + 40)", got)
	}
	if got := byName["payment-api"]["selfTimeMs"]; got != float64(110) {
		t.Errorf("payment-api self time = %v, want 110 (250 minus what it waited on)", got)
	}
	// Biggest first: the answer to "where did the time go" is the first row.
	if first, _ := services[0].(map[string]any); first["service"] != "ledger" {
		t.Errorf("rollup starts with %v, want ledger", first["service"])
	}
	if byName["payment-api"]["errorCount"] != float64(1) {
		t.Errorf("payment-api errorCount = %v, want 1", byName["payment-api"]["errorCount"])
	}
}

// Concurrent children can outlast their parent's own clock. Self time floors
// at zero rather than going negative, which would corrupt the rollup.
func TestGetTraceSelfTimeNeverGoesNegative(t *testing.T) {
	f := fakeWithServices("a")
	f.Traces = map[string]storage.Trace{"x": {TraceID: "x", Spans: []storage.Span{
		{SpanID: "root", Service: "a", Duration: 10 * time.Millisecond, StartTime: testNow},
		{SpanID: "c1", ParentSpanID: "root", Service: "b", Duration: 30 * time.Millisecond, StartTime: testNow},
	}}}
	payload, _ := callTool(t, serverWith(f), "get_trace", `{"trace_id":"x"}`)
	for _, sv := range payload["services"].([]any) {
		row, _ := sv.(map[string]any)
		if row["selfTimeMs"].(float64) < 0 {
			t.Errorf("negative self time: %v", row)
		}
	}
}

func TestGetTraceNotFound(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "get_trace", `{"trace_id":"nope"}`)
	if !isErr {
		t.Fatalf("missing trace accepted: %v", payload)
	}
	if payload["error"] == nil {
		t.Errorf("no message: %v", payload)
	}
}

func TestSearchLogs(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.LogPage = storage.LogPage{Logs: []storage.LogRecord{{
		Timestamp: testNow, Severity: "ERROR", Service: "payment-api",
		Body: "connection refused talking to ledger", TraceID: "abc", SpanID: "s1",
	}}}
	payload, isErr := callTool(t, serverWith(f), "search_logs",
		`{"service":"payment-api","level":"ERROR","query":"refused","window":"30m"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	logs, _ := payload["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1: %v", len(logs), payload)
	}
	first, _ := logs[0].(map[string]any)
	// The body is returned in full. Redacting it would make the tool useless
	// for the job it exists to do — the line you would mask is invariably the
	// one that explains the failure. The opt-in module and the audit line are
	// what carry that decision (design/2026-09-01-mcp-server.md).
	if first["body"] != "connection refused talking to ledger" {
		t.Errorf("body = %v", first["body"])
	}
	if first["traceId"] != "abc" {
		t.Errorf("traceId = %v — correlation is the point of returning logs here", first["traceId"])
	}
	if f.LastLogQuery.MinSeverity != "ERROR" || f.LastLogQuery.Query != "refused" {
		t.Errorf("filters did not reach storage: %+v", f.LastLogQuery)
	}
}

// A trace id takes the correlated path, not the search path: it is a different
// store call, and the one that answers "what did this request log".
func TestSearchLogsByTraceID(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.TraceLogs = map[string][]storage.LogRecord{"abc": {{
		Timestamp: testNow, Severity: "ERROR", Service: "payment-api", Body: "boom", TraceID: "abc"}}}
	payload, isErr := callTool(t, serverWith(f), "search_logs", `{"trace_id":"abc"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	logs, _ := payload["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want the 1 on this trace: %v", len(logs), payload)
	}
}

func TestListErrorIssues(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.Issues = []storage.ErrorIssue{{
		Fingerprint: 0xdeadbeef, Service: "payment-api", Type: "ConnectionError",
		Message: "connection refused", Source: "span", Status: "unresolved",
		Count: 42, FirstSeen: testNow.Add(-time.Hour), LastSeen: testNow, LastTraceID: "abc",
	}}
	payload, isErr := callTool(t, serverWith(f), "list_error_issues", `{"service":"payment-api"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	issues, _ := payload["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(issues), payload)
	}
	first, _ := issues[0].(map[string]any)
	if first["type"] != "ConnectionError" || first["count"] != float64(42) {
		t.Errorf("issue row = %v", first)
	}
	// Hex, the same wire form the REST API uses, so an id an agent reads here
	// pastes into a URL a human can open.
	if first["fingerprint"] != "00000000deadbeef" {
		t.Errorf("fingerprint = %v, want the hex form", first["fingerprint"])
	}
	if first["lastTraceId"] != "abc" {
		t.Errorf("lastTraceId = %v — it is the bridge to get_trace", first["lastTraceId"])
	}
	// Open issues by default: an agent asking what is broken is not asking
	// what used to be.
	if f.LastIssueQuery.Status != "unresolved" {
		t.Errorf("default status = %q, want unresolved", f.LastIssueQuery.Status)
	}
}
