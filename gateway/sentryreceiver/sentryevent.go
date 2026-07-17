package sentryreceiver

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// sentryEvent is the subset of the Sentry "event" item we consume. Unknown
// fields are ignored; missing fields degrade gracefully.
type sentryEvent struct {
	EventID     string                 `json:"event_id"`
	Timestamp   json.RawMessage        `json:"timestamp"` // float epoch seconds OR RFC3339
	Platform    string                 `json:"platform"`
	Level       string                 `json:"level"`
	Logger      string                 `json:"logger"`
	Environment string                 `json:"environment"`
	Release     string                 `json:"release"`
	ServerName  string                 `json:"server_name"`
	Message     json.RawMessage        `json:"message"` // string OR {formatted,message}
	Exception   *sentryException       `json:"exception"`
	SDK         *sentrySDK             `json:"sdk"`
	Contexts    map[string]interface{} `json:"contexts"`
	Tags        map[string]string      `json:"tags"`
	Request     *sentryRequest         `json:"request"`
	Transaction string                 `json:"transaction"`
}

type sentryException struct {
	Values []sentryExceptionValue `json:"values"`
}

type sentryExceptionValue struct {
	Type       string            `json:"type"`
	Value      string            `json:"value"`
	Module     string            `json:"module"`
	Stacktrace *sentryStacktrace `json:"stacktrace"`
}

type sentryStacktrace struct {
	Frames []sentryFrame `json:"frames"`
}

// sentryFrame is one stack frame. Sentry orders frames oldest-first, so
// rendering reverses them to the conventional newest-first (crash site on top).
type sentryFrame struct {
	Filename string `json:"filename"`
	AbsPath  string `json:"abs_path"`
	Function string `json:"function"`
	Module   string `json:"module"`
	Lineno   int    `json:"lineno"`
	Colno    int    `json:"colno"`
}

type sentrySDK struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type sentryRequest struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

// eventTime parses the Sentry timestamp (float epoch seconds or RFC3339),
// falling back to the provided now on absence/parse failure.
func (e *sentryEvent) eventTime(now time.Time) time.Time {
	raw := strings.TrimSpace(string(e.Timestamp))
	if raw == "" || raw == "null" {
		return now
	}
	if strings.HasPrefix(raw, "\"") {
		var s string
		if err := json.Unmarshal(e.Timestamp, &s); err == nil {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC()
			}
		}
		return now
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	}
	return now
}

// messageText extracts the plain message string ("" if absent). Sentry allows
// either a bare string or a {message, formatted} object.
func (e *sentryEvent) messageText() string {
	raw := strings.TrimSpace(string(e.Message))
	if raw == "" || raw == "null" {
		return ""
	}
	if strings.HasPrefix(raw, "\"") {
		var s string
		_ = json.Unmarshal(e.Message, &s)
		return s
	}
	var obj struct {
		Formatted string `json:"formatted"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(e.Message, &obj); err == nil {
		if obj.Formatted != "" {
			return obj.Formatted
		}
		return obj.Message
	}
	return ""
}
