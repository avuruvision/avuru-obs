package sentryreceiver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustEvent(t *testing.T, payload string) sentryEvent {
	t.Helper()
	var ev sentryEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return ev
}

func TestNormalizeException(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	ev := mustEvent(t, `{
		"event_id":"abc","level":"error","environment":"prod","release":"web@1","platform":"javascript",
		"sdk":{"name":"sentry.javascript.browser","version":"7.100.0"},
		"exception":{"values":[{"type":"TypeError","value":"undefined is not a function",
			"stacktrace":{"frames":[
				{"filename":"router.js","function":"route","lineno":120,"colno":1},
				{"abs_path":"https://x/app.js","function":"render","lineno":12,"colno":3}]}}]},
		"contexts":{"trace":{"trace_id":"1234567890ABCDEF1234567890abcdef","span_id":"1234567890abcdef"}},
		"tags":{"page":"checkout"}}`)

	n := normalizeEvent(ev, "web", now)

	if n.SeverityNumber != 17 || n.SeverityText != "ERROR" {
		t.Errorf("severity wrong: %d/%s", n.SeverityNumber, n.SeverityText)
	}
	if n.ServiceName != "web" || n.Environment != "prod" {
		t.Errorf("service/env wrong: %+v", n)
	}
	if n.Body != "TypeError: undefined is not a function" {
		t.Errorf("body wrong: %q", n.Body)
	}
	if n.LogAttributes["avuru.error.source"] != "sentry" {
		t.Errorf("source tag missing: %+v", n.LogAttributes)
	}
	if n.LogAttributes["exception.type"] != "TypeError" {
		t.Errorf("exception.type wrong")
	}
	if n.LogAttributes["sentry.sdk.name"] != "sentry.javascript.browser" {
		t.Errorf("sdk name missing")
	}
	if n.LogAttributes["sentry.tag.page"] != "checkout" {
		t.Errorf("tag not mapped: %+v", n.LogAttributes)
	}
	// trace id lowercased and validated.
	if n.TraceID != "1234567890abcdef1234567890abcdef" || n.SpanID != "1234567890abcdef" {
		t.Errorf("trace context wrong: %q / %q", n.TraceID, n.SpanID)
	}
	// Stacktrace rendered newest-first (crash site on top).
	lines := strings.Split(n.LogAttributes["exception.stacktrace"], "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "render") {
		t.Errorf("stacktrace order/format wrong: %q", n.LogAttributes["exception.stacktrace"])
	}
}

func TestNormalizeLevels(t *testing.T) {
	cases := map[string]struct {
		num  int32
		text string
	}{
		"fatal":   {21, "FATAL"},
		"error":   {17, "ERROR"},
		"warning": {13, "WARN"},
		"info":    {9, "INFO"},
		"debug":   {5, "DEBUG"},
		"weird":   {17, "ERROR"}, // unknown → ERROR
	}
	for level, want := range cases {
		n := normalizeEvent(sentryEvent{Level: level}, "svc", time.Unix(0, 0))
		if n.SeverityNumber != want.num || n.SeverityText != want.text {
			t.Errorf("level %q → %d/%s, want %d/%s", level, n.SeverityNumber, n.SeverityText, want.num, want.text)
		}
	}
}

func TestNormalizeInvalidTraceContextDropped(t *testing.T) {
	ev := mustEvent(t, `{"level":"error","message":"x","contexts":{"trace":{"trace_id":"tooshort","span_id":"nope"}}}`)
	n := normalizeEvent(ev, "svc", time.Unix(0, 0))
	if n.TraceID != "" || n.SpanID != "" {
		t.Errorf("invalid trace ids should be dropped: %q / %q", n.TraceID, n.SpanID)
	}
	if n.Body != "x" {
		t.Errorf("message fallback wrong: %q", n.Body)
	}
}

func TestEventTimeFormats(t *testing.T) {
	now := time.Unix(999, 0).UTC()
	// Float epoch seconds.
	ev := mustEvent(t, `{"timestamp":1700000000.5,"level":"error"}`)
	if got := ev.eventTime(now); got.Unix() != 1700000000 {
		t.Errorf("float epoch: got %v", got)
	}
	// RFC3339 string.
	ev = mustEvent(t, `{"timestamp":"2026-01-02T03:04:05Z","level":"error"}`)
	if got := ev.eventTime(now); got.Year() != 2026 || got.Hour() != 3 {
		t.Errorf("rfc3339: got %v", got)
	}
	// Absent → now.
	ev = mustEvent(t, `{"level":"error"}`)
	if got := ev.eventTime(now); !got.Equal(now) {
		t.Errorf("absent timestamp should fall back to now: got %v", got)
	}
}
