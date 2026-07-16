package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestSearchErrorIssuesEndpoint(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &storagetest.Fake{
		Issues: []storage.ErrorIssue{{
			Fingerprint: 0xdeadbeef, Service: "web", Type: "NullPointerException",
			Message: "boom", Source: "span", Status: "resolved", Regressed: true,
			FirstSeen: now.Add(-time.Hour), LastSeen: now, Count: 42, LastTraceID: "t1",
		}},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/errors/issues?status=unresolved&service=web&q=null&sort=count&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := fake.LastIssueQuery
	if q.Status != "unresolved" || q.Service != "web" || q.Query != "null" || q.Sort != "count" || q.Limit != 10 {
		t.Errorf("query not parsed: %+v", q)
	}
	if q.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", q.Tenant)
	}

	var resp errorIssuesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 {
		t.Fatalf("issues = %+v", resp.Issues)
	}
	iss := resp.Issues[0]
	if iss.Fingerprint != "00000000deadbeef" {
		t.Errorf("fingerprint hex = %q, want zero-padded", iss.Fingerprint)
	}
	if !iss.Regressed || iss.Count != 42 || iss.LastTraceID != "t1" {
		t.Errorf("issue DTO wrong: %+v", iss)
	}
}

func TestGetErrorIssueRoundTrip(t *testing.T) {
	fake := &storagetest.Fake{Issue: storage.ErrorIssue{Fingerprint: 0xff, Service: "api", Type: "Timeout"}}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/errors/issues/00000000000000ff")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var iss errorIssueDTO
	if err := json.NewDecoder(rec.Body).Decode(&iss); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if iss.Fingerprint != "00000000000000ff" || iss.Service != "api" {
		t.Errorf("issue wrong: %+v", iss)
	}

	// A non-hex fingerprint is a 400.
	if rec := get(t, mux, "/api/v1/errors/issues/nothex"); rec.Code != http.StatusBadRequest {
		t.Errorf("non-hex fingerprint: got %d, want 400", rec.Code)
	}

	// A missing issue is a 404.
	fake.IssueErr = storage.ErrNotFound
	if rec := get(t, mux, "/api/v1/errors/issues/00000000000000ff"); rec.Code != http.StatusNotFound {
		t.Errorf("missing issue: got %d, want 404", rec.Code)
	}
}

func TestErrorEventsPagination(t *testing.T) {
	now := time.Now().UTC()
	fake := &storagetest.Fake{
		EventPage: storage.ErrorEventPage{
			Events: []storage.ErrorEvent{{
				Timestamp: now, Service: "web", Type: "NPE", Message: "boom",
				Stacktrace: "at x", TraceID: "t1", SpanID: "s1", Source: "span",
			}},
			NextCursor: &storage.ErrorEventCursor{Timestamp: now, TraceID: "t1", SpanID: "s1"},
		},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/errors/issues/00000000deadbeef/events?limit=25")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LastEventQuery.Fingerprint != 0xdeadbeef || fake.LastEventQuery.Limit != 25 {
		t.Errorf("event query wrong: %+v", fake.LastEventQuery)
	}
	var resp errorEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].TraceID != "t1" || resp.NextCursor == "" {
		t.Errorf("events response wrong: %+v", resp)
	}

	// The returned cursor must round-trip into a query.
	rec2 := get(t, mux, "/api/v1/errors/issues/00000000deadbeef/events?cursor="+resp.NextCursor)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cursor round-trip: %d", rec2.Code)
	}
	if c := fake.LastEventQuery.Cursor; c == nil || c.TraceID != "t1" || c.SpanID != "s1" {
		t.Errorf("cursor not round-tripped: %+v", fake.LastEventQuery.Cursor)
	}
}

func TestErrorHistogramEndpoint(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	fake := &storagetest.Fake{Histogram: []storage.ErrorHistogramPoint{{Time: now, Count: 5}}}
	mux := newMux(fake)

	var resp errorHistogramResponse
	rec := get(t, mux, "/api/v1/errors/issues/00000000deadbeef/histogram?points=30")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Points) != 1 || resp.Points[0].Count != 5 {
		t.Errorf("histogram wrong: %+v", resp)
	}
}

// TestErrorTrackingRoutesGated proves the error-tracking routes are absent when
// the module is off (404), and present when on.
func TestErrorTrackingRoutesGated(t *testing.T) {
	paths := []string{
		"/api/v1/errors/issues",
		"/api/v1/errors/issues/00000000deadbeef",
		"/api/v1/errors/issues/00000000deadbeef/events",
		"/api/v1/errors/issues/00000000deadbeef/histogram",
	}

	off, _ := modules.Parse("core")
	muxOff := http.NewServeMux()
	Register(muxOff, func() storage.Store { return &storagetest.Fake{} }, Config{Modules: off})
	for _, p := range paths {
		if rec := get(t, muxOff, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s with module off: got %d, want 404", p, rec.Code)
		}
	}

	on := newMux(&storagetest.Fake{})
	for _, p := range paths {
		if rec := get(t, on, p); rec.Code == http.StatusNotFound {
			t.Errorf("%s with module on: 404 (route missing)", p)
		}
	}
}
