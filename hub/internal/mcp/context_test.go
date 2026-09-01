package mcp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

func contextFake() *storagetest.Fake {
	return &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "payment-api", SpanCount: 3600, ErrorCount: 360, P50: 10 * time.Millisecond,
				P95: 220 * time.Millisecond, P99: 900 * time.Millisecond},
			{Name: "frontend", SpanCount: 7200},
			{Name: "ledger", SpanCount: 3600, ErrorCount: 360},
		},
		Edges: []storage.ServiceEdge{
			{Source: "frontend", Target: "payment-api", Count: 3600, ErrorCount: 360, P95: 240 * time.Millisecond},
			{Source: "payment-api", Target: "ledger", Count: 3600, ErrorCount: 360, P95: 200 * time.Millisecond},
			{Source: "frontend", Target: "ledger", Count: 10}, // not this service's business
		},
		Issues: []storage.ErrorIssue{{
			Fingerprint: 1, Service: "payment-api", Type: "ConnectionError",
			Message: "connection refused", Status: "unresolved", Count: 360,
			FirstSeen: testNow.Add(-time.Hour), LastSeen: testNow, LastTraceID: "abc",
		}},
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "payments-degraded", Target: "payments", Status: "firing", Since: testNow.Add(-10 * time.Minute)},
			{Tenant: "default", RuleName: "quiet", Target: "cart", Status: "ok"},
		},
	}
}

// The whole reason this tool exists: the picture a human starts with, in one
// call. Given a bare tool per endpoint a model opens an investigation by
// guessing, and spends five round trips assembling what one query knows.
func TestServiceContextAnswersInOneCall(t *testing.T) {
	payload, isErr := callTool(t, serverWith(contextFake()), "service_context", `{"service":"payment-api","window":"1h"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	if payload["service"] != "payment-api" {
		t.Fatalf("service = %v", payload["service"])
	}
	red, _ := payload["red"].(map[string]any)
	if red["errorRate"] != 0.1 {
		t.Errorf("errorRate = %v, want 0.1", red["errorRate"])
	}
	if red["ratePerSec"] != float64(1) {
		t.Errorf("ratePerSec = %v, want 1 (3600 over an hour)", red["ratePerSec"])
	}
	if red["p95Ms"] != float64(220) {
		t.Errorf("p95Ms = %v, want 220", red["p95Ms"])
	}

	callers, _ := payload["callers"].([]any)
	if len(callers) != 1 {
		t.Fatalf("got %d callers, want 1: %v", len(callers), callers)
	}
	if c, _ := callers[0].(map[string]any); c["service"] != "frontend" || c["p95Ms"] != float64(240) {
		t.Errorf("caller = %v, want frontend at 240ms client-side", c)
	}

	deps, _ := payload["dependencies"].([]any)
	if len(deps) != 1 {
		t.Fatalf("got %d dependencies, want 1: %v", len(deps), deps)
	}
	if d, _ := deps[0].(map[string]any); d["service"] != "ledger" || d["errorRate"] != 0.1 {
		t.Errorf("dependency = %v, want ledger at a 10%% error rate", d)
	}

	issues, _ := payload["topIssues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	alerts, _ := payload["firingAlerts"].([]any)
	if len(alerts) != 1 {
		t.Fatalf("got %d firing alerts, want 1 (the resolved one is not firing)", len(alerts))
	}
	if a, _ := alerts[0].(map[string]any); a["rule"] != "payments-degraded" {
		t.Errorf("alert = %v", a)
	}
}

// A section that is missing because a module is off must SAY so. Silence reads
// as "there are no issues", which is a different and much worse claim.
func TestServiceContextNamesWhatItCouldNotRead(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(contextFake())
	s.Modules = core

	payload, isErr := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	if _, present := payload["topIssues"]; present {
		t.Error("topIssues present with error-tracking off — absent is the honest shape")
	}
	notes, _ := payload["notes"].([]any)
	if len(notes) == 0 {
		t.Fatal("no notes: a module that is off has to be named, not silently omitted")
	}
	joined := fmt.Sprint(notes...)
	for _, want := range []string{"error-tracking", "alerting", "logs"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not mention %q: %v", want, notes)
		}
	}
}

// A mesh proxy on a dependency list is a claimed relationship between services
// that never talk to each other. It is reported with its role rather than
// dropped: dropping it on a meshed install leaves an empty list, which reads
// as "this service depends on nothing".
func TestServiceContextLabelsTransportNeighbours(t *testing.T) {
	f := contextFake()
	f.Services = append(f.Services, storage.ServiceStats{Name: "istio-ingressgateway", SpanCount: 7200})
	f.Edges = append(f.Edges, storage.ServiceEdge{Source: "istio-ingressgateway", Target: "payment-api", Count: 100})
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	payload, _ := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	callers, _ := payload["callers"].([]any)
	var gateway map[string]any
	for _, c := range callers {
		row, _ := c.(map[string]any)
		if row["service"] == "istio-ingressgateway" {
			gateway = row
		}
	}
	if gateway == nil {
		t.Fatalf("the gateway is missing from callers: %v", callers)
	}
	if gateway["role"] != "transport" {
		t.Errorf("role = %v, want transport", gateway["role"])
	}
}

func TestServiceContextUnknownService(t *testing.T) {
	payload, isErr := callTool(t, serverWith(contextFake()), "service_context", `{"service":"payment_api"}`)
	if !isErr {
		t.Fatalf("unknown service accepted: %v", payload)
	}
	hints, _ := payload["didYouMean"].([]any)
	if len(hints) == 0 {
		t.Errorf("no near matches offered: %v", payload)
	}
}
