// compatsend sends ONE deterministic telemetry fixture to a gateway over a
// legacy ingest protocol, so the wider-ingest receivers (Jaeger, Zipkin,
// Prometheus remote-write v2, Loki push) can be conformance-tested end to end
// (e2e/compat_test.go drives it; `make e2e-compat` owns the stack).
//
// The jaeger and zipkin fixtures encode the SAME logical span — service
// -service, operation "compat-op", 100ms, status OK — so a test can assert
// protocol parity against an OTLP send of that span. `promrw-v1` deliberately
// sends a v1 WriteRequest: the receiver is v2-only and must answer HTTP 415.
//
// Exit is non-zero on any non-2xx (or gRPC error), with the status in the
// message, so a caller can assert rejections (401 under enforce, 415 for v1).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	fixtureOperation = "compat-op"
	fixtureDuration  = 100 * time.Millisecond
	sendTimeout      = 15 * time.Second
)

// fixtureStart places the span inside every default query window while keeping
// the 100ms duration exact (only *relative* times are asserted).
func fixtureStart() time.Time { return time.Now().Add(-time.Minute) }

func main() {
	proto := flag.String("proto", "", "jaeger | zipkin | promrw | promrw-v1 | loki")
	endpoint := flag.String("endpoint", "", "target: host:port for jaeger (gRPC), base URL for the rest")
	key := flag.String("key", "", "ingest key, sent as Authorization: Bearer <key>")
	service := flag.String("service", "compat-probe", "service.name (jaeger/zipkin) / job (promrw)")
	traceID := flag.String("trace-id", "c0badc0ffee000000000000000000001", "32-hex trace id (jaeger/zipkin); fixed default for grep-ability")
	metric := flag.String("metric", "compat_probe_gauge", "metric name (promrw)")
	label := flag.String("label", "service=compat-probe", "loki stream label as key=value")
	line := flag.String("line", "compat conformance log line", "loki log line body")
	flag.Parse()

	if *endpoint == "" {
		fatalf("-endpoint is required")
	}

	var err error
	switch *proto {
	case "jaeger":
		err = sendJaeger(*endpoint, *key, *service, *traceID)
	case "zipkin":
		err = sendZipkin(*endpoint, *key, *service, *traceID)
	case "promrw":
		err = sendPromRWv2(*endpoint, *key, *service, *metric)
	case "promrw-v1":
		err = sendPromRWv1(*endpoint, *key, *service, *metric)
	case "loki":
		err = sendLoki(*endpoint, *key, *label, *line)
	default:
		fatalf("-proto must be jaeger, zipkin, promrw, promrw-v1 or loki (got %q)", *proto)
	}
	if err != nil {
		fatalf("%s: %v", *proto, err)
	}
	fmt.Printf("compatsend %s: ok\n", *proto)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "compatsend "+format+"\n", args...)
	os.Exit(1)
}

// postExpect2xx POSTs the body and fails with the status line in the error so
// callers can assert exact rejection codes (401/415).
func postExpect2xx(url, contentType, key string, headers map[string]string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: sendTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
