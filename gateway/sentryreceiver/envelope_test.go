package sentryreceiver

import (
	"strings"
	"testing"
)

// A realistic browser-SDK envelope: header line, item header (with length),
// then the event payload.
const browserEnvelope = `{"event_id":"9ec79c33ec9942 ab8353589fcb2e04c","dsn":"http://key@host/1"}
{"type":"event","length":0}
{"event_id":"9ec79c33","platform":"javascript","level":"error","environment":"production","release":"web@1.2.3","sdk":{"name":"sentry.javascript.browser","version":"7.100.0"},"exception":{"values":[{"type":"TypeError","value":"undefined is not a function","stacktrace":{"frames":[{"filename":"router.js","function":"route","lineno":120,"colno":1},{"filename":"app.js","abs_path":"https://x/app.js","function":"render","lineno":12,"colno":3}]}}]},"contexts":{"trace":{"trace_id":"1234567890abcdef1234567890abcdef","span_id":"1234567890abcdef"}}}`

func TestParseEnvelopeBrowserEvent(t *testing.T) {
	// Fix the length header to match the payload (last line).
	lines := strings.SplitN(browserEnvelope, "\n", 3)
	payload := lines[2]
	body := lines[0] + "\n{\"type\":\"event\",\"length\":" + itoa(len(payload)) + "}\n" + payload

	events, err := parseEnvelope([]byte(body))
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Platform != "javascript" || ev.Level != "error" {
		t.Errorf("event fields wrong: %+v", ev)
	}
	if firstException(ev).Type != "TypeError" {
		t.Errorf("exception type wrong: %+v", firstException(ev))
	}
}

func TestParseEnvelopeNoLength(t *testing.T) {
	// Newline-delimited payload (no length field).
	body := `{"event_id":"a"}
{"type":"event"}
{"level":"fatal","message":"boom"}`
	events, err := parseEnvelope([]byte(body))
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if len(events) != 1 || events[0].Level != "fatal" {
		t.Fatalf("events wrong: %+v", events)
	}
}

func TestParseEnvelopeSkipsNonEventItems(t *testing.T) {
	// A session item then an event item — only the event is returned, and the
	// unknown type never errors.
	body := `{"event_id":"a"}
{"type":"session"}
{"sid":"1","status":"ok"}
{"type":"event"}
{"level":"error","message":"real"}`
	events, err := parseEnvelope([]byte(body))
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if len(events) != 1 || events[0].messageText() != "real" {
		t.Fatalf("expected only the event item: %+v", events)
	}
}

func TestParseEnvelopeMalformedHeader(t *testing.T) {
	if _, err := parseEnvelope([]byte("not json\n")); err == nil {
		t.Error("expected error on malformed envelope header")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
