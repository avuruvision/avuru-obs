package mcp

import (
	"bytes"
	"encoding/json"
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

// The mesh collapse, for this client too.
//
// A proxy on a dependency list is a claimed relationship between services that
// never talk to each other. The service map has reported the dependency
// underneath the hop since v0.9; this tool labelled the proxy instead, so an
// agent and a person asking the same question got different answers.
func TestServiceContextCollapsesTransportNeighbours(t *testing.T) {
	f := contextFake()
	f.Services = append(f.Services,
		storage.ServiceStats{Name: "istio-ingressgateway", SpanCount: 7200, TransportEvidence: true})
	// The two halves of one hop, as the hub reports them.
	f.Edges = append(f.Edges,
		storage.ServiceEdge{Source: "checkout", Target: "istio-ingressgateway", Count: 100},
		storage.ServiceEdge{Source: "istio-ingressgateway", Target: "payment-api", Count: 100})
	// And the dependency the ancestry walk recovers from them.
	f.Collapsed = []storage.ServiceEdge{{
		Source: "checkout", Target: "payment-api", Count: 100,
		CollapsedCount: 100, ViaTransport: []string{"istio-ingressgateway"},
	}}
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	payload, _ := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	callers, _ := payload["callers"].([]any)

	var checkout map[string]any
	for _, c := range callers {
		row, _ := c.(map[string]any)
		if row["service"] == "istio-ingressgateway" {
			t.Fatalf("the proxy is still reported as a caller: %v", row)
		}
		if row["service"] == "checkout" {
			checkout = row
		}
	}
	if checkout == nil {
		t.Fatalf("the dependency behind the proxy is missing: %v", callers)
	}
	// A reconstructed edge must never read as a directly observed one.
	via, _ := checkout["viaTransport"].([]any)
	if len(via) != 1 || via[0] != "istio-ingressgateway" {
		t.Errorf("viaTransport = %v, want the proxy named", checkout["viaTransport"])
	}
	if checkout["collapsedCalls"] != float64(100) {
		t.Errorf("collapsedCalls = %v, want 100", checkout["collapsedCalls"])
	}
}

// The classifier must run BEFORE the query: the ancestry walk needs to know
// which workloads to step over. Mirrors the service map's own assertion.
func TestServiceContextClassifiesBeforeItRecovers(t *testing.T) {
	f := contextFake()
	f.Services = append(f.Services,
		storage.ServiceStats{Name: "istio-ingressgateway", SpanCount: 7200, TransportEvidence: true})
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	callTool(t, s, "service_context", `{"service":"payment-api"}`)
	if len(f.LastCollapseTransport) != 1 || f.LastCollapseTransport[0] != "istio-ingressgateway" {
		t.Fatalf("transport passed to the walk = %v, want the gateway", f.LastCollapseTransport)
	}
}

// An unmeshed install must pay nothing and look exactly as it did: no proxies,
// so no query, and no viaTransport anywhere in the answer.
func TestServiceContextCostsNothingWithoutAMesh(t *testing.T) {
	f := contextFake()
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	payload, _ := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	if len(f.LastCollapseTransport) != 0 {
		t.Errorf("queried the walk with %v on an unmeshed install", f.LastCollapseTransport)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("viaTransport")) {
		t.Errorf("viaTransport appeared without a mesh: %s", raw)
	}
}

// The tool describes ONE service. If that service is itself a proxy, hiding
// transport would empty the answer being asked for.
func TestServiceContextKeepsTheProxyWhenItIsTheSubject(t *testing.T) {
	f := contextFake()
	f.Services = append(f.Services,
		storage.ServiceStats{Name: "istio-ingressgateway", SpanCount: 7200, TransportEvidence: true})
	f.Edges = append(f.Edges,
		storage.ServiceEdge{Source: "checkout", Target: "istio-ingressgateway", Count: 100},
		storage.ServiceEdge{Source: "istio-ingressgateway", Target: "payment-api", Count: 100})
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	payload, _ := callTool(t, s, "service_context", `{"service":"istio-ingressgateway"}`)
	callers, _ := payload["callers"].([]any)
	deps, _ := payload["dependencies"].([]any)
	if len(callers) != 1 || len(deps) != 1 {
		t.Fatalf("a proxy's own page lost its neighbourhood: callers=%v deps=%v", callers, deps)
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
