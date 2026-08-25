package checks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

// The span has to reach the GATEWAY, as a sender would send it — not be written
// into ClickHouse behind the pipeline's back. Asserting on the wire format is
// how that stays true.
func TestEmitterPostsOTLPToTheGateway(t *testing.T) {
	var got ptraceotlp.ExportRequest
	var path, auth, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, ctype = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		got = ptraceotlp.NewExportRequest()
		if err := got.UnmarshalProto(body); err != nil {
			t.Errorf("gateway received something that is not OTLP: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	em := NewOTLPEmitter(srv.URL, "avuruk_test", "avuru-obs-checks", quietLogger())
	traceID, spanID := em.EmitCheckSpan(context.Background(), Result{
		CheckID: "core-login", Group: "core", Tenant: "default",
		At: time.Now(), Latency: 12 * time.Millisecond, OK: false,
		Status: 503, Error: "expected a 2xx, got 503",
	}, "https://app.example.com/health")

	if path != "/v1/traces" {
		t.Errorf("posted to %q, want the OTLP traces path", path)
	}
	if ctype != "application/x-protobuf" {
		t.Errorf("content-type = %q", ctype)
	}
	// Under enforce mode the hub is a sender like any other, and needs a key.
	if auth != "Bearer avuruk_test" {
		t.Errorf("authorization = %q, want the ingest key", auth)
	}
	if traceID == "" || spanID == "" {
		t.Fatal("no ids returned — the stored result cannot point at the span")
	}

	td := got.Traces()
	if td.SpanCount() != 1 {
		t.Fatalf("span count = %d, want 1", td.SpanCount())
	}
	span := td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	if span.Name() != "check core-login" {
		t.Errorf("span name = %q", span.Name())
	}
	if v, ok := span.Attributes().Get("avuru.check.id"); !ok || v.Str() != "core-login" {
		t.Error("span does not carry the check id")
	}
	// A failed probe must be a failed span, or it will not read as an error
	// anywhere the trace is shown.
	if span.Status().Code().String() != "Error" {
		t.Errorf("status = %s, want Error for a failed probe", span.Status().Code())
	}
	res := td.ResourceSpans().At(0).Resource().Attributes()
	if v, ok := res.Get("avuru.synthetic"); !ok || v.Str() != "check" {
		t.Error("check traffic is not marked synthetic")
	}
}

// A gateway that is down must not stop a check recording that the thing it
// watches is down — that would be the failure taking out the alarm.
func TestEmitterIsBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	em := NewOTLPEmitter(srv.URL, "", "svc", quietLogger())
	traceID, _ := em.EmitCheckSpan(context.Background(), Result{CheckID: "c", At: time.Now()}, "https://x/health")
	if traceID != "" {
		t.Error("returned a trace id the gateway never accepted")
	}
}

// No gateway configured is a supported shape, not an error: the results still
// stand on their own.
func TestNoEndpointMeansNoEmitter(t *testing.T) {
	if em := NewOTLPEmitter("", "", "svc", quietLogger()); em != nil {
		t.Error("built an emitter with no endpoint")
	}
}
