package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

const defaultRange = 15 * time.Minute

// parseTimeRange reads start/end (RFC3339); both absent → last 15 minutes.
func parseTimeRange(r *http.Request) (storage.TimeRange, error) {
	now := time.Now().UTC()
	tr := storage.TimeRange{Start: now.Add(-defaultRange), End: now}

	if s := r.URL.Query().Get("start"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return tr, badRequest("invalid start: %v", err)
		}
		tr.Start = t
	}
	if e := r.URL.Query().Get("end"); e != "" {
		t, err := time.Parse(time.RFC3339, e)
		if err != nil {
			return tr, badRequest("invalid end: %v", err)
		}
		tr.End = t
	}
	if !tr.End.After(tr.Start) {
		return tr, badRequest("end must be after start")
	}
	return tr, nil
}

func parseInt(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, badRequest("invalid %s: %v", name, err)
	}
	return n, nil
}

// parseBool reads a "true"/"1" flag; anything else (incl. absent) is def.
func parseBool(r *http.Request, name string, def bool) bool {
	switch r.URL.Query().Get(name) {
	case "":
		return def
	case "true", "1":
		return true
	default:
		return false
	}
}

// parseTags reads `tags=key=value,key2=value2` into a map of span-attribute
// equality filters. Blank or malformed pairs (missing '=') are skipped.
func parseTags(r *http.Request) map[string]string {
	v := r.URL.Query().Get("tags")
	if v == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(val)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseDurationMs(r *http.Request, name string) (time.Duration, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	ms, err := strconv.ParseFloat(v, 64)
	if err != nil || ms < 0 {
		return 0, badRequest("invalid %s: must be milliseconds", name)
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// Cursor wire format: base64("<unix nanos>,<duration nanos>,<traceId>"). Both
// sort keys are carried (timestamp and root-span duration) so the cursor works
// for any Order; the TraceId tiebreaker is last so commas in it survive.
func encodeCursor(c *storage.TraceCursor) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf("%d,%d,%s", c.Timestamp.UnixNano(), c.Duration.Nanoseconds(), c.TraceID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseCursor(r *http.Request) (*storage.TraceCursor, error) {
	v := r.URL.Query().Get("cursor")
	if v == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	parts := strings.SplitN(string(raw), ",", 3)
	if len(parts) != 3 {
		return nil, badRequest("invalid cursor")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	dur, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	return &storage.TraceCursor{
		Timestamp: time.Unix(0, ns).UTC(),
		Duration:  time.Duration(dur),
		TraceID:   parts[2],
	}, nil
}

// tenant resolves the request tenant (single-tenant in OSS; header is the
// enterprise seam).
func tenant(r *http.Request) string {
	if t := r.Header.Get("X-Avuru-Tenant"); t != "" {
		return t
	}
	return storage.DefaultTenant
}
