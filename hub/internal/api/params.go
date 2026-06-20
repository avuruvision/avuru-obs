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

// Cursor wire format: base64("<unix nanos>,<traceId>"). Nanosecond precision
// plus the TraceId tiebreaker are both required for gapless keyset paging.
func encodeCursor(c *storage.TraceCursor) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf("%d,%s", c.Timestamp.UnixNano(), c.TraceID)
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
	parts := strings.SplitN(string(raw), ",", 2)
	if len(parts) != 2 {
		return nil, badRequest("invalid cursor")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	return &storage.TraceCursor{Timestamp: time.Unix(0, ns).UTC(), TraceID: parts[1]}, nil
}

// tenant resolves the request tenant (single-tenant in OSS; header is the
// enterprise seam).
func tenant(r *http.Request) string {
	if t := r.Header.Get("X-Avuru-Tenant"); t != "" {
		return t
	}
	return storage.DefaultTenant
}
