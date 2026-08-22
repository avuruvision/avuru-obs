//go:build e2e

// Protocol conformance for the wider-ingest receivers (I-4) against the
// compose stack, driven through tools/compatsend so the tool itself is
// exercised, not a reimplementation of it.
//
// OPT-IN: needs the compat-e2e overlay (all four receivers + enforce auth +
// forward-sink) and runs only when AVURUOBS_E2E_COMPAT=1, so the default
// `make e2e` run is unaffected. `make e2e-compat` owns the lifecycle; see
// deploy/compose/docker-compose.compat-e2e.yaml for the manual recipe.
//
// What is proven, per protocol:
//   - Jaeger/Zipkin: the SAME logical span sent via OTLP, Jaeger gRPC and
//     Zipkin v2 JSON lands three times with identical operation, status and
//     duration — protocol translation loses nothing the hub surfaces.
//   - Remote-write: a v2 gauge sample lands in otel_metrics_gauge under the
//     key's project; a v1 sender is refused with HTTP 415 (the receiver is
//     v2-only at this collector line — an explicit product decision).
//   - Loki: stream labels come back as log-RECORD attributes, not resource
//     attributes (so ServiceName stays empty — Loki labels carry no resource
//     identity).
//   - Enforce: unkeyed zipkin/loki/remote-write sends are all 401s.
//   - Dual-write: the Jaeger-ingested trace id shows up in the forward-sink
//     collector's debug logs — otlp/forward really fans out.
package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// Seeded by the overlay for project "compat".
	compatKey     = "avuruk_compat000000000000000000000000"
	compatProject = "compat"
	compatService = "compat-probe"
	compatOp      = "compat-op"

	// Fixed ids for grep-ability (fresh stack per run — `make e2e-compat`
	// tears down with -v, so fixed ids never collide across runs).
	compatOTLPTraceID   = "cafe0001bbbb2222cccc3333dddd4444"
	compatJaegerTraceID = "cafe0002bbbb2222cccc3333dddd4444"
	compatZipkinTraceID = "cafe0003bbbb2222cccc3333dddd4444"

	compatMetric   = "compat_probe_gauge"
	compatLokiLine = "compat conformance log line"
)

// Endpoints match the compat-e2e overlay's remapped host ports.
var (
	compatOTLP   = envOr("AVURUOBS_E2E_COMPAT_OTLP", "http://localhost:24318")
	compatJaeger = envOr("AVURUOBS_E2E_COMPAT_JAEGER", "localhost:24250")
	compatZipkin = envOr("AVURUOBS_E2E_COMPAT_ZIPKIN", "http://localhost:29411")
	compatPromRW = envOr("AVURUOBS_E2E_COMPAT_PROMRW", "http://localhost:29291")
	compatLoki   = envOr("AVURUOBS_E2E_COMPAT_LOKI", "http://localhost:23100")
	// Container whose logs prove dual-write; name follows the -p project name.
	compatSink = envOr("AVURUOBS_E2E_COMPAT_SINK", "avuru-compat-e2e-forward-sink-1")
)

func requireCompatE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("AVURUOBS_E2E_COMPAT") != "1" {
		t.Skip("compat e2e needs the compat-e2e overlay; set AVURUOBS_E2E_COMPAT=1 (make e2e-compat)")
	}
}

// compatsend runs the sender tool once. Combined output comes back either way
// so rejection tests can assert on the status in the error message.
func compatsend(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Dir = "../tools/compatsend"
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func compatsendOK(t *testing.T, args ...string) {
	t.Helper()
	if out, err := compatsend(t, args...); err != nil {
		t.Fatalf("compatsend %v failed: %v\n%s", args, err, out)
	}
}

// sendCompatOTLP posts the parity baseline: the same logical span the jaeger
// and zipkin fixtures encode (operation compat-op, 100ms, status OK), via
// OTLP/HTTP with the ingest key.
func sendCompatOTLP(t *testing.T) {
	t.Helper()
	start := time.Now().Add(-time.Minute).UnixNano()
	payload := fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[
	  {"key":"service.name","value":{"stringValue":%q}}]},
	  "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"name":%q,"kind":2,
	  "startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":1}}]}]}]}`,
		compatService, compatOTLPTraceID, compatOTLPTraceID[16:], compatOp,
		start, start+100_000_000)
	req, err := http.NewRequest(http.MethodPost, compatOTLP+"/v1/traces", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+compatKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OTLP baseline send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OTLP baseline send returned %d, want 200", resp.StatusCode)
	}
}

type compatSpan struct {
	Service    string  `json:"service"`
	Operation  string  `json:"operation"`
	Status     string  `json:"statusCode"`
	DurationMs float64 `json:"durationMs"`
}

type compatTraceResp struct {
	Spans []compatSpan `json:"spans"`
}

// fetchCompatSpan polls until the trace is visible under the key's project and
// returns its compat-op span.
func fetchCompatSpan(t *testing.T, proto, traceID string) compatSpan {
	t.Helper()
	var found compatSpan
	poll(t, 60*time.Second, func() error {
		var resp compatTraceResp
		code, err := getJSONTenant("/api/v1/traces/"+traceID, compatProject, &resp)
		if code != http.StatusOK {
			return fmt.Errorf("%s trace %s not visible under %q yet: code=%d err=%v",
				proto, traceID, compatProject, code, err)
		}
		for _, s := range resp.Spans {
			if s.Operation == compatOp {
				found = s
				return nil
			}
		}
		return fmt.Errorf("%s trace %s has no %q span yet: %+v", proto, traceID, compatOp, resp.Spans)
	})
	return found
}

// TestCompatTraceParity: one logical span, three protocols, identical
// hub-visible facts.
func TestCompatTraceParity(t *testing.T) {
	requireCompatE2E(t)

	sendCompatOTLP(t)
	compatsendOK(t, "-proto", "jaeger", "-endpoint", compatJaeger,
		"-key", compatKey, "-service", compatService, "-trace-id", compatJaegerTraceID)
	compatsendOK(t, "-proto", "zipkin", "-endpoint", compatZipkin,
		"-key", compatKey, "-service", compatService, "-trace-id", compatZipkinTraceID)

	spans := map[string]compatSpan{
		"otlp":   fetchCompatSpan(t, "otlp", compatOTLPTraceID),
		"jaeger": fetchCompatSpan(t, "jaeger", compatJaegerTraceID),
		"zipkin": fetchCompatSpan(t, "zipkin", compatZipkinTraceID),
	}
	for proto, s := range spans {
		if s.Service != compatService {
			t.Errorf("%s: service = %q, want %q", proto, s.Service, compatService)
		}
		if s.Status != "Ok" {
			t.Errorf("%s: status = %q, want Ok — the translator dropped the status", proto, s.Status)
		}
		// The fixture is exactly 100ms in every protocol's native unit.
		if s.DurationMs < 99.5 || s.DurationMs > 100.5 {
			t.Errorf("%s: duration = %.3fms, want 100ms", proto, s.DurationMs)
		}
	}
}

// chCount runs a count() query against the compat stack's ClickHouse.
func chCount(t *testing.T, query string) (int, error) {
	t.Helper()
	u := chURL + "/?user=avuru&password=avuru&query=" + url.QueryEscape(query)
	resp, err := http.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var n int
	if _, err := fmt.Fscan(resp.Body, &n); err != nil {
		return 0, fmt.Errorf("parsing count: %w", err)
	}
	return n, nil
}

// TestCompatPromRWv2 asserts a remote-write v2 gauge sample lands in
// otel_metrics_gauge under the key's project, with job mapped to ServiceName.
func TestCompatPromRWv2(t *testing.T) {
	requireCompatE2E(t)

	compatsendOK(t, "-proto", "promrw", "-endpoint", compatPromRW,
		"-key", compatKey, "-service", compatService, "-metric", compatMetric)

	query := fmt.Sprintf(
		"SELECT count() FROM otel.otel_metrics_gauge WHERE MetricName = '%s' AND Tenant = '%s' AND ServiceName = '%s'",
		compatMetric, compatProject, compatService,
	)
	poll(t, 60*time.Second, func() error {
		n, err := chCount(t, query)
		if err != nil {
			return err
		}
		if n < 1 {
			return fmt.Errorf("no %s rows under tenant %q yet", compatMetric, compatProject)
		}
		return nil
	})
}

// TestCompatPromRWv1Rejected: the receiver is v2-only — a v1 WriteRequest must
// be refused with 415, not silently dropped or misparsed.
func TestCompatPromRWv1Rejected(t *testing.T) {
	requireCompatE2E(t)

	out, err := compatsend(t, "-proto", "promrw-v1", "-endpoint", compatPromRW,
		"-key", compatKey, "-service", compatService, "-metric", compatMetric)
	if err == nil {
		t.Fatalf("v1 remote-write was ACCEPTED — the receiver must be v2-only\n%s", out)
	}
	if !strings.Contains(out, "HTTP 415") {
		t.Fatalf("v1 remote-write rejected but not with 415:\n%s", out)
	}
}

type compatLogsResp struct {
	Logs []struct {
		Service    string            `json:"service"`
		Body       string            `json:"body"`
		Attributes map[string]string `json:"attributes"`
	} `json:"logs"`
}

// TestCompatLokiPush: the pushed line surfaces through the hub logs API with
// the stream label as a RECORD attribute — and no resource identity.
func TestCompatLokiPush(t *testing.T) {
	requireCompatE2E(t)

	compatsendOK(t, "-proto", "loki", "-endpoint", compatLoki,
		"-key", compatKey, "-label", "service="+compatService, "-line", compatLokiLine)

	// Search by body substring: Loki labels are record attributes, so the
	// ServiceName-backed service filter cannot find this log by design.
	path := "/api/v1/logs?q=" + url.QueryEscape(compatLokiLine) +
		"&start=" + queryStart() + "&end=" + queryEnd()
	poll(t, 60*time.Second, func() error {
		var resp compatLogsResp
		if _, err := getJSONTenant(path, compatProject, &resp); err != nil {
			return err
		}
		for _, l := range resp.Logs {
			if !strings.Contains(l.Body, compatLokiLine) {
				continue
			}
			if got := l.Attributes["service"]; got != compatService {
				return fmt.Errorf("log found but record attribute service = %q, want %q (attrs %v)",
					got, compatService, l.Attributes)
			}
			if l.Service != "" {
				return fmt.Errorf("log carries resource service %q — Loki labels must NOT become resource attributes", l.Service)
			}
			return nil
		}
		return fmt.Errorf("pushed loki line not visible under %q yet", compatProject)
	})
}

// TestCompatEnforceRejectsUnkeyed: every HTTP compat receiver refuses an
// unkeyed send in enforce mode — the credential is required on the new
// protocols exactly as on OTLP.
func TestCompatEnforceRejectsUnkeyed(t *testing.T) {
	requireCompatE2E(t)

	cases := map[string][]string{
		"zipkin": {"-proto", "zipkin", "-endpoint", compatZipkin,
			"-service", compatService, "-trace-id", "cafe0009bbbb2222cccc3333dddd4444"},
		"promrw": {"-proto", "promrw", "-endpoint", compatPromRW,
			"-service", compatService, "-metric", compatMetric},
		"loki": {"-proto", "loki", "-endpoint", compatLoki,
			"-label", "service=" + compatService, "-line", "unkeyed must not land"},
	}
	for proto, args := range cases {
		out, err := compatsend(t, args...)
		if err == nil {
			t.Errorf("%s: unkeyed send was ACCEPTED under enforce\n%s", proto, out)
			continue
		}
		if !strings.Contains(out, "HTTP 401") && !strings.Contains(out, "HTTP 403") {
			t.Errorf("%s: unkeyed send rejected but not with 401/403:\n%s", proto, out)
		}
	}
}

// TestCompatDualWriteForwards: the Jaeger-ingested span's trace id must appear
// in the forward-sink's debug logs — proof the otlp/forward exporter fans out
// what the compat receivers ingest.
func TestCompatDualWriteForwards(t *testing.T) {
	requireCompatE2E(t)

	// Runs after TestCompatTraceParity in file order; re-send defensively so
	// this test also stands alone (-run CompatDualWrite).
	compatsendOK(t, "-proto", "jaeger", "-endpoint", compatJaeger,
		"-key", compatKey, "-service", compatService, "-trace-id", compatJaegerTraceID)

	poll(t, 60*time.Second, func() error {
		out, err := exec.Command("docker", "logs", compatSink).CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker logs %s: %v (%s)", compatSink, err, out)
		}
		if !strings.Contains(string(out), compatJaegerTraceID) {
			return fmt.Errorf("trace %s not in forward-sink logs yet", compatJaegerTraceID)
		}
		return nil
	})
}
