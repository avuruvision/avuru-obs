package api

// Error-tracking endpoints. The handler file is error_tracking.go, not
// errors.go — that name holds the central error wrapper.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Fingerprints are 64-bit; the wire form is lowercase hex so URLs stay compact
// and stable.
func fingerprintHex(fp uint64) string { return fmt.Sprintf("%016x", fp) }

func parseFingerprint(r *http.Request) (uint64, error) {
	v := r.PathValue("fingerprint")
	fp, err := strconv.ParseUint(v, 16, 64)
	if err != nil {
		return 0, badRequest("invalid fingerprint %q", v)
	}
	return fp, nil
}

type errorIssueDTO struct {
	Fingerprint string    `json:"fingerprint"`
	Service     string    `json:"service"`
	Type        string    `json:"type"`
	Message     string    `json:"message"`
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Regressed   bool      `json:"regressed"`
	FirstSeen   time.Time `json:"firstSeen"`
	LastSeen    time.Time `json:"lastSeen"`
	Count       uint64    `json:"count"`
	LastTraceID string    `json:"lastTraceId,omitempty"`
}

func toErrorIssueDTO(i storage.ErrorIssue) errorIssueDTO {
	return errorIssueDTO{
		Fingerprint: fingerprintHex(i.Fingerprint),
		Service:     i.Service,
		Type:        i.Type,
		Message:     i.Message,
		Source:      i.Source,
		Status:      i.Status,
		Regressed:   i.Regressed,
		FirstSeen:   i.FirstSeen.UTC(),
		LastSeen:    i.LastSeen.UTC(),
		Count:       i.Count,
		LastTraceID: i.LastTraceID,
	}
}

type errorIssuesResponse struct {
	Issues []errorIssueDTO `json:"issues"`
}

type errorEventDTO struct {
	Timestamp   time.Time         `json:"timestamp"`
	Service     string            `json:"service"`
	Type        string            `json:"type"`
	Message     string            `json:"message"`
	Stacktrace  string            `json:"stacktrace,omitempty"`
	TraceID     string            `json:"traceId,omitempty"`
	SpanID      string            `json:"spanId,omitempty"`
	Source      string            `json:"source"`
	Environment string            `json:"environment,omitempty"`
	SdkName     string            `json:"sdkName,omitempty"`
	SdkVersion  string            `json:"sdkVersion,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type errorEventsResponse struct {
	Events     []errorEventDTO `json:"events"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type histogramPointDTO struct {
	Time  time.Time `json:"time"`
	Count uint64    `json:"count"`
}

type errorHistogramResponse struct {
	Points []histogramPointDTO `json:"points"`
}

func (a *API) handleSearchErrorIssues(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 100)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	issues, err := store.SearchErrorIssues(r.Context(), storage.ErrorIssueQuery{
		Tenant:  tenant,
		Range:   tr,
		Status:  r.URL.Query().Get("status"),
		Service: r.URL.Query().Get("service"),
		Query:   r.URL.Query().Get("q"),
		Sort:    r.URL.Query().Get("sort"),
		Limit:   limit,
	})
	if err != nil {
		return err
	}
	resp := errorIssuesResponse{Issues: make([]errorIssueDTO, 0, len(issues))}
	for _, i := range issues {
		resp.Issues = append(resp.Issues, toErrorIssueDTO(i))
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) handleGetErrorIssue(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	fp, err := parseFingerprint(r)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	issue, err := store.GetErrorIssue(r.Context(), tenant, fp)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toErrorIssueDTO(issue))
	return nil
}

func (a *API) handleListErrorEvents(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	fp, err := parseFingerprint(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 50)
	if err != nil {
		return err
	}
	cursor, err := parseErrorEventCursor(r)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	page, err := store.ListErrorEvents(r.Context(), storage.ErrorEventQuery{
		Tenant:      tenant,
		Fingerprint: fp,
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		return err
	}
	resp := errorEventsResponse{
		Events:     make([]errorEventDTO, 0, len(page.Events)),
		NextCursor: encodeErrorEventCursor(page.NextCursor),
	}
	for _, e := range page.Events {
		resp.Events = append(resp.Events, errorEventDTO{
			Timestamp:   e.Timestamp.UTC(),
			Service:     e.Service,
			Type:        e.Type,
			Message:     e.Message,
			Stacktrace:  e.Stacktrace,
			TraceID:     e.TraceID,
			SpanID:      e.SpanID,
			Source:      e.Source,
			Environment: e.Environment,
			SdkName:     e.SdkName,
			SdkVersion:  e.SdkVersion,
			Attributes:  e.Attributes,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (a *API) handleErrorIssueHistogram(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	fp, err := parseFingerprint(r)
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	points, err := parseInt(r, "points", 60)
	if err != nil {
		return err
	}
	tenant, err := a.project(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	buckets, err := store.ErrorIssueHistogram(r.Context(), tenant, fp, tr, points)
	if err != nil {
		return err
	}
	resp := errorHistogramResponse{Points: make([]histogramPointDTO, 0, len(buckets))}
	for _, b := range buckets {
		resp.Points = append(resp.Points, histogramPointDTO{Time: b.Time.UTC(), Count: b.Count})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type setStatusRequest struct {
	Status string `json:"status"`
}

var validTriageStatus = map[string]bool{"unresolved": true, "resolved": true, "ignored": true}

// handleSetErrorIssueStatus records a triage decision and returns the updated
// issue. Resolving a regressed issue simply writes a newer resolved row, which
// resets the read-time regression comparison — no special case.
func (a *API) handleSetErrorIssueStatus(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	fp, err := parseFingerprint(r)
	if err != nil {
		return err
	}
	var body setStatusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		return decodeJSONError(err)
	}
	if !validTriageStatus[body.Status] {
		return badRequest("invalid status %q (want unresolved|resolved|ignored)", body.Status)
	}
	t, err := a.project(r, auth.RoleEditor)
	if err != nil {
		return err
	}
	if err := store.SetErrorIssueStatus(r.Context(), t, fp, body.Status); err != nil {
		return err
	}
	// Return the issue as it now reads, so the client reflects the effective
	// status (including a reset of regression).
	issue, err := store.GetErrorIssue(r.Context(), t, fp)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toErrorIssueDTO(issue))
	return nil
}

// Cursor wire format mirrors logs: base64("<unix nanos>,<traceId>,<spanId>").
func encodeErrorEventCursor(c *storage.ErrorEventCursor) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf("%d,%s,%s", c.Timestamp.UnixNano(), c.TraceID, c.SpanID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func parseErrorEventCursor(r *http.Request) (*storage.ErrorEventCursor, error) {
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
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, badRequest("invalid cursor")
	}
	return &storage.ErrorEventCursor{
		Timestamp: time.Unix(0, nanos).UTC(),
		TraceID:   parts[1],
		SpanID:    parts[2],
	}, nil
}
