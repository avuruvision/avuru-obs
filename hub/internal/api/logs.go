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

type logRecordDTO struct {
	Timestamp  time.Time         `json:"timestamp"`
	Severity   string            `json:"severity"`
	Service    string            `json:"service"`
	Body       string            `json:"body"`
	TraceID    string            `json:"traceId,omitempty"`
	SpanID     string            `json:"spanId,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type logsResponse struct {
	Logs       []logRecordDTO `json:"logs"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

func toLogRecordDTO(l storage.LogRecord) logRecordDTO {
	return logRecordDTO{
		Timestamp:  l.Timestamp.UTC(),
		Severity:   l.Severity,
		Service:    l.Service,
		Body:       l.Body,
		TraceID:    l.TraceID,
		SpanID:     l.SpanID,
		Attributes: l.Attributes,
	}
}

func (a *API) handleSearchLogs(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 50)
	if err != nil {
		return err
	}
	cursor, err := parseLogCursor(r)
	if err != nil {
		return err
	}
	page, err := store.SearchLogs(r.Context(), storage.LogQuery{
		Tenant:      tenant(r),
		Range:       tr,
		Service:     r.URL.Query().Get("service"),
		MinSeverity: r.URL.Query().Get("severity"),
		Query:       r.URL.Query().Get("q"),
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		return err
	}
	resp := logsResponse{Logs: make([]logRecordDTO, 0, len(page.Logs)), NextCursor: encodeLogCursor(page.NextCursor)}
	for _, l := range page.Logs {
		resp.Logs = append(resp.Logs, toLogRecordDTO(l))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) handleLogsForTrace(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	traceID := r.PathValue("traceId")
	if traceID == "" {
		return badRequest("missing traceId")
	}
	logs, err := store.LogsForTrace(r.Context(), tenant(r), traceID)
	if err != nil {
		return err
	}
	resp := logsResponse{Logs: make([]logRecordDTO, 0, len(logs))}
	for _, l := range logs {
		resp.Logs = append(resp.Logs, toLogRecordDTO(l))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// Log cursor wire format: base64("<unix nanos>,<traceId>,<spanId>"). W3C trace
// and span ids are hex, so a plain 3-way split is safe.
func encodeLogCursor(c *storage.LogCursor) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf("%d,%s,%s", c.Timestamp.UnixNano(), c.TraceID, c.SpanID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseLogCursor(r *http.Request) (*storage.LogCursor, error) {
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
	return &storage.LogCursor{Timestamp: time.Unix(0, ns).UTC(), TraceID: parts[1], SpanID: parts[2]}, nil
}
