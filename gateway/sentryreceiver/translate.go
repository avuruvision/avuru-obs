package sentryreceiver

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// normalizedError is the OTel-shaped projection of a Sentry event: everything
// needed to build a log record, as plain Go so the mapping is unit-testable
// without the collector runtime. otel.go turns this into plog.Logs.
type normalizedError struct {
	Timestamp      time.Time
	SeverityNumber int32
	SeverityText   string
	Body           string
	ServiceName    string
	Environment    string
	TraceID        string // 32 hex, or ""
	SpanID         string // 16 hex, or ""
	LogAttributes  map[string]string
}

var traceIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
var spanIDRe = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)

// severity maps a Sentry level to an OTel (number, text) pair. Unknown levels
// default to ERROR — an event reaching the error pipeline is an error unless
// it says otherwise.
func severity(level string) (int32, string) {
	switch strings.ToLower(level) {
	case "fatal":
		return 21, "FATAL"
	case "warning":
		return 13, "WARN"
	case "info":
		return 9, "INFO"
	case "debug":
		return 5, "DEBUG"
	default:
		return 17, "ERROR"
	}
}

// normalizeEvent maps a Sentry event to normalizedError. serviceName is the
// resolved service (project config → server_name → sentry-project-<id>).
func normalizeEvent(ev sentryEvent, serviceName string, now time.Time) normalizedError {
	num, text := severity(ev.Level)

	attrs := map[string]string{
		"avuru.error.source": "sentry",
	}
	if ev.EventID != "" {
		attrs["sentry.event_id"] = ev.EventID
	}
	if ev.Platform != "" {
		attrs["sentry.platform"] = ev.Platform
	}
	if ev.Release != "" {
		attrs["sentry.release"] = ev.Release
	}
	if ev.SDK != nil {
		if ev.SDK.Name != "" {
			attrs["sentry.sdk.name"] = ev.SDK.Name
		}
		if ev.SDK.Version != "" {
			attrs["sentry.sdk.version"] = ev.SDK.Version
		}
	}
	if ev.Request != nil && ev.Request.URL != "" {
		attrs["url.full"] = ev.Request.URL
	}
	for k, v := range ev.Tags {
		attrs["sentry.tag."+k] = v
	}

	body := ev.messageText()
	if exc := firstException(ev); exc != nil {
		attrs["exception.type"] = exc.Type
		attrs["exception.message"] = exc.Value
		if st := renderStacktrace(exc.Stacktrace); st != "" {
			attrs["exception.stacktrace"] = st
		}
		if body == "" {
			body = strings.TrimSpace(exc.Type + ": " + exc.Value)
		}
	}
	if body == "" {
		body = "sentry event " + ev.EventID
	}

	traceID, spanID := traceContext(ev)

	return normalizedError{
		Timestamp:      ev.eventTime(now),
		SeverityNumber: num,
		SeverityText:   text,
		Body:           body,
		ServiceName:    serviceName,
		Environment:    ev.Environment,
		TraceID:        traceID,
		SpanID:         spanID,
		LogAttributes:  attrs,
	}
}

// firstException returns the last exception in the chain (the one that was
// actually thrown — Sentry lists causes first, the thrown exception last).
func firstException(ev sentryEvent) *sentryExceptionValue {
	if ev.Exception == nil || len(ev.Exception.Values) == 0 {
		return nil
	}
	return &ev.Exception.Values[len(ev.Exception.Values)-1]
}

// renderStacktrace turns structured frames into a text trace, newest frame
// first (crash site on top), matching the OTel exception.stacktrace convention
// used by the derivation MVs.
func renderStacktrace(st *sentryStacktrace) string {
	if st == nil || len(st.Frames) == 0 {
		return ""
	}
	var b strings.Builder
	for i := len(st.Frames) - 1; i >= 0; i-- {
		f := st.Frames[i]
		loc := f.AbsPath
		if loc == "" {
			loc = f.Filename
		}
		fn := f.Function
		if fn == "" {
			fn = "?"
		}
		fmt.Fprintf(&b, "  at %s (%s:%d:%d)\n", fn, loc, f.Lineno, f.Colno)
	}
	return strings.TrimRight(b.String(), "\n")
}

// traceContext extracts a validated trace/span id from event.contexts.trace.
func traceContext(ev sentryEvent) (traceID, spanID string) {
	raw, ok := ev.Contexts["trace"].(map[string]interface{})
	if !ok {
		return "", ""
	}
	if v, ok := raw["trace_id"].(string); ok && traceIDRe.MatchString(v) {
		traceID = strings.ToLower(v)
	}
	if v, ok := raw["span_id"].(string); ok && spanIDRe.MatchString(v) {
		spanID = strings.ToLower(v)
	}
	return traceID, spanID
}
