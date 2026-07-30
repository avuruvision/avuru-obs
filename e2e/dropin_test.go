//go:build e2e

// End-to-end tests against the running compose stack (`make e2e` owns the
// lifecycle: up → seed → test → down).
//
// Guards the drop-in product promise (agent_docs/architecture.md): an
// OTel-SDK app configured ONLY via OTEL_EXPORTER_OTLP_ENDPOINT (HotROD) must
// surface in the hub with no other change.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// Endpoints default to the compose stack's published ports (hub is 18080:8080
// in docker-compose); each is env-overridable so a shared dev machine can run
// the suite against an isolated compose project with remapped host ports (the
// Playwright AVURUOBS_BASE_URL precedent). CI sets nothing and keeps the defaults.
var (
	hubURL    = envOr("AVURUOBS_E2E_HUB_URL", "http://localhost:18080")
	hotrodURL = envOr("AVURUOBS_E2E_HOTROD_URL", "http://localhost:8088")
	chURL     = envOr("AVURUOBS_E2E_CH_URL", "http://localhost:8123")
)

const seededTraceID = "aaaa1111bbbb2222cccc3333dddd4444"

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// apiClient is the shared, authenticated client used by every helper here
// (getJSON, getJSONTenant) and by any test hitting the hub directly. The hub
// has auth ON by default (Task 8), so plain http.DefaultClient now gets 401s
// against every hub route except /healthz and /api/v1/auth/*. TestMain logs
// in as admin before any test runs and replaces this with the authenticated
// client; tests that deliberately want to be unauthenticated use
// http.DefaultClient (or http.Get) directly instead of this var.
var apiClient = http.DefaultClient

// TestMain authenticates once for the whole package before running any
// test: the hub may still be bootstrapping the admin user (bootstrapAdmin
// waits for its ClickHouse connection, then creates `admin` with
// AVURUOBS_AUTH_ADMIN_PASSWORD — see the Makefile's `e2e` target) when the
// suite starts, so login is retried rather than attempted once.
func TestMain(m *testing.M) {
	client, err := waitForAdminLogin(60 * time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not log in as admin against %s within 60s: %v\n", hubURL, err)
		os.Exit(1)
	}
	apiClient = client
	os.Exit(m.Run())
}

// waitForAdminLogin mirrors poll's retry pattern (2s interval, no busy loop)
// but can't use poll directly: TestMain has no *testing.T to hand it.
func waitForAdminLogin(timeout time.Duration) (*http.Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		client, err := loginAs("admin", adminPassword)
		if err == nil {
			return client, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(2 * time.Second)
	}
}

// poll retries fn until it returns nil or the deadline passes (never sleep
// blindly — agent_docs/testing.md).
func poll(t *testing.T, timeout time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = fn(); lastErr == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("condition not met within %v: %v", timeout, lastErr)
}

func getJSON(path string, out any) error {
	resp, err := apiClient.Get(hubURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type traceSummary struct {
	TraceID    string  `json:"traceId"`
	SpanCount  int     `json:"spanCount"`
	ErrorCount int     `json:"errorCount"`
	DurationMs float64 `json:"durationMs"`
}

type tracesResp struct {
	Traces []traceSummary `json:"traces"`
}

type span struct {
	Service   string `json:"service"`
	Operation string `json:"operation"`
	ParentID  string `json:"parentSpanId"`
	Status    string `json:"statusCode"`
}

type traceResp struct {
	TraceID string `json:"traceId"`
	Spans   []span `json:"spans"`
}

type logRecord struct {
	Severity string `json:"severity"`
	Service  string `json:"service"`
	TraceID  string `json:"traceId"`
}

type logsResp struct {
	Logs []logRecord `json:"logs"`
}

// TestJaegerDropIn: HotROD (Jaeger's own demo, stock OTel SDK, endpoint env
// var only) → gateway → ClickHouse → hub API.
func TestJaegerDropIn(t *testing.T) {
	// Generate traffic.
	for i := 0; i < 3; i++ {
		resp, err := http.Get(hotrodURL + "/dispatch?customer=123&nonse=e2e" + fmt.Sprint(i))
		if err != nil {
			t.Fatalf("driving hotrod: %v", err)
		}
		resp.Body.Close()
	}

	var found traceSummary
	poll(t, 60*time.Second, func() error {
		var resp tracesResp
		if err := getJSON("/api/v1/traces?service=frontend&limit=10", &resp); err != nil {
			return err
		}
		for _, tr := range resp.Traces {
			if tr.SpanCount > 5 {
				found = tr
				return nil
			}
		}
		return fmt.Errorf("no multi-span frontend trace yet (%d traces)", len(resp.Traces))
	})

	var full traceResp
	if err := getJSON("/api/v1/traces/"+found.TraceID, &full); err != nil {
		t.Fatalf("fetching trace: %v", err)
	}
	services := map[string]bool{}
	for _, s := range full.Spans {
		services[s.Service] = true
	}
	if len(services) < 3 {
		t.Errorf("expected a cross-service trace, got services %v", services)
	}
}

// TestSeededDeterminism: the seeded fixture must come back EXACTLY —
// fixed trace id, 3 spans, 1 error span, known structure.
func TestSeededDeterminism(t *testing.T) {
	var full traceResp
	poll(t, 30*time.Second, func() error {
		return getJSON("/api/v1/traces/"+seededTraceID, &full)
	})

	if len(full.Spans) != 3 {
		t.Fatalf("seeded trace has %d spans, want 3: %+v", len(full.Spans), full.Spans)
	}
	byOp := map[string]span{}
	roots := 0
	errors := 0
	for _, s := range full.Spans {
		byOp[s.Operation] = s
		if s.ParentID == "" {
			roots++
		}
		if s.Status == "Error" {
			errors++
		}
		if s.Service != "seed-checkout" {
			t.Errorf("unexpected service %q", s.Service)
		}
	}
	if roots != 1 {
		t.Errorf("got %d roots, want 1", roots)
	}
	if errors != 2 { // root + SQL span are both Error in the fixture
		t.Errorf("got %d error spans, want 2", errors)
	}
	for _, op := range []string{"POST /checkout", "SELECT orders", "GET cache"} {
		if _, ok := byOp[op]; !ok {
			t.Errorf("missing span %q (have %v)", op, byOp)
		}
	}

	// Summary endpoint must agree.
	var resp tracesResp
	if err := getJSON("/api/v1/traces?service=seed-checkout", &resp); err != nil {
		t.Fatalf("searching seeded traces: %v", err)
	}
	if len(resp.Traces) != 1 {
		t.Fatalf("got %d seed-checkout traces, want exactly 1", len(resp.Traces))
	}
	got := resp.Traces[0]
	if got.TraceID != seededTraceID || got.SpanCount != 3 || got.ErrorCount != 2 {
		t.Errorf("summary mismatch: %+v", got)
	}
	if got.DurationMs < 249 || got.DurationMs > 251 { // fixture root = exactly 250ms
		t.Errorf("root duration %.2fms, want 250ms", got.DurationMs)
	}
}

// TestSeededLogs: correlated logs land in ClickHouse AND surface through the
// hub logs API (M2). The fixture has 2 logs on the seeded trace.
func TestSeededLogs(t *testing.T) {
	// 1. Raw landing in ClickHouse.
	query := fmt.Sprintf(
		"SELECT count() FROM otel.otel_logs WHERE ServiceName = 'seed-checkout' AND TraceId = '%s'",
		seededTraceID,
	)
	poll(t, 30*time.Second, func() error {
		u := chURL + "/?user=avuru&password=avuru&query=" + url.QueryEscape(query)
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var n int
		if _, err := fmt.Fscan(resp.Body, &n); err != nil {
			return fmt.Errorf("parsing count: %w", err)
		}
		if n != 2 {
			return fmt.Errorf("got %d correlated logs, want 2", n)
		}
		return nil
	})

	// 2. Hub logs API: trace correlation.
	var byTrace logsResp
	if err := getJSON("/api/v1/traces/"+seededTraceID+"/logs", &byTrace); err != nil {
		t.Fatalf("logs-for-trace API: %v", err)
	}
	if len(byTrace.Logs) != 2 {
		t.Fatalf("logs-for-trace: got %d, want 2 (%+v)", len(byTrace.Logs), byTrace.Logs)
	}

	// 3. Hub logs API: severity filter (one ERROR in the fixture).
	var errLogs logsResp
	if err := getJSON("/api/v1/logs?service=seed-checkout&severity=ERROR", &errLogs); err != nil {
		t.Fatalf("logs search API: %v", err)
	}
	if len(errLogs.Logs) != 1 || errLogs.Logs[0].Severity != "ERROR" {
		t.Fatalf("severity filter: got %+v, want one ERROR", errLogs.Logs)
	}
}

// (UI serving moved out of the hub — the SPA is a separate nginx deployable,
// covered by `make e2e-ui` / the single-origin compose smoke.)
