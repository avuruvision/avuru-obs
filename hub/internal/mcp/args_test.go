package mcp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

var testNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestTimeRange(t *testing.T) {
	tests := []struct {
		name        string
		args        windowArgs
		wantStart   time.Time
		wantEnd     time.Time
		wantToolErr bool
	}{
		{"absent means the last hour", windowArgs{}, testNow.Add(-time.Hour), testNow, false},
		{"relative window", windowArgs{Window: "15m"}, testNow.Add(-15 * time.Minute), testNow, false},
		{"a day", windowArgs{Window: "24h"}, testNow.Add(-24 * time.Hour), testNow, false},
		{"absolute pair", windowArgs{
			Start: "2026-09-01T09:00:00Z", End: "2026-09-01T10:00:00Z"},
			time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), false},
		{"end alone anchors the window", windowArgs{Window: "30m", End: "2026-09-01T10:00:00Z"},
			time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC), time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), false},
		{"unparseable window", windowArgs{Window: "last tuesday"}, time.Time{}, time.Time{}, true},
		{"negative window", windowArgs{Window: "-1h"}, time.Time{}, time.Time{}, true},
		{"unparseable start", windowArgs{Start: "yesterday"}, time.Time{}, time.Time{}, true},
		{"end before start", windowArgs{
			Start: "2026-09-01T10:00:00Z", End: "2026-09-01T09:00:00Z"}, time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.args.timeRange(testNow)
			var terr *toolError
			if tt.wantToolErr {
				if !errors.As(err, &terr) {
					t.Fatalf("err = %v, want a toolError the model can read", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Start.Equal(tt.wantStart) || !got.End.Equal(tt.wantEnd) {
				t.Errorf("range = %s..%s, want %s..%s", got.Start, got.End, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestClampRows(t *testing.T) {
	tests := []struct{ in, want int }{
		{0, defaultRows}, {-3, defaultRows}, {5, 5}, {maxRows, maxRows}, {maxRows + 1, maxRows}, {100000, maxRows},
	}
	for _, tt := range tests {
		if got := clampRows(tt.in, defaultRows, maxRows); got != tt.want {
			t.Errorf("clampRows(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func fakeWithServices(names ...string) *storagetest.Fake {
	f := &storagetest.Fake{}
	for _, n := range names {
		f.Services = append(f.Services, storage.ServiceStats{Name: n, SpanCount: 10})
	}
	return f
}

func serverWith(f *storagetest.Fake) *Server {
	return &Server{Store: f, Modules: modules.AllSet(), Tenant: "default",
		Tenants: []string{"default"}, Version: "test", Now: func() time.Time { return testNow }}
}

func TestResolveService(t *testing.T) {
	s := serverWith(fakeWithServices("payment-api", "payments-worker", "frontend"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	if got, err := s.resolveService(context.Background(), tr, "payment-api"); err != nil || got.Name != "payment-api" {
		t.Fatalf("exact match: got %q, %v", got.Name, err)
	}
	// The stored spelling wins, so everything downstream filters on a name the
	// store will actually match.
	if got, err := s.resolveService(context.Background(), tr, "Payment-API"); err != nil || got.Name != "payment-api" {
		t.Fatalf("case-insensitive match: got %q, %v", got.Name, err)
	}
}

// The rule this feature turns on: an unknown name is an ERROR naming the near
// matches, never an empty list. A model handed [] concludes the service is
// dead and reports that with confidence.
func TestResolveServiceUnknownNamesNearMatches(t *testing.T) {
	s := serverWith(fakeWithServices("payment-api", "payments-worker", "frontend"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	_, err := s.resolveService(context.Background(), tr, "paiment-api")
	var terr *toolError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want a toolError", err)
	}
	if len(terr.DidYouMean) == 0 || terr.DidYouMean[0] != "payment-api" {
		t.Errorf("didYouMean = %v, want payment-api first", terr.DidYouMean)
	}
	if terr.Message == "" {
		t.Error("the error must say what was not found, and over which window")
	}
}

func TestNearest(t *testing.T) {
	known := []string{"payment-api", "payments-worker", "frontend", "cart"}
	tests := []struct {
		name string
		want []string
		in   string
	}{
		{"a typo finds the neighbour", []string{"payment-api"}, "paymnet-api"},
		{"a prefix finds both", []string{"payment-api", "payments-worker"}, "payment"},
		{"nothing close suggests nothing", nil, "zzzzzzzzzzzzzz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nearest(tt.in, known, 5); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nearest(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// fakeWithLogOnly is fakeWithServices' counterpart: names known ONLY from the
// signal probe, i.e. workloads with no entry spans.
func fakeWithLogOnly(f *storagetest.Fake, names ...string) *storagetest.Fake {
	for _, n := range names {
		f.Presence = append(f.Presence, storage.ServicePresence{
			Name: n, LogRecords: 1240, ErrorLogRecords: 15, LastSeen: testNow,
		})
	}
	return f
}

// TestResolveServiceAcceptsAServiceKnownOnlyFromLogs is the bug this fixes. On
// a real estate, search_logs with no filter returned rows for "hotrod" while
// search_logs FOR "hotrod" answered "no service named hotrod reported
// anything" — a false statement about a workload with 15 open issues, made by
// the one code path whose docstring warns against exactly that.
func TestResolveServiceAcceptsAServiceKnownOnlyFromLogs(t *testing.T) {
	s := serverWith(fakeWithLogOnly(fakeWithServices("payment-api", "frontend"), "hotrod"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	got, err := s.resolveService(context.Background(), tr, "hotrod")
	if err != nil {
		t.Fatalf("a service with logs and no entry spans was rejected: %v", err)
	}
	if got.Name != "hotrod" {
		t.Errorf("name = %q, want the stored spelling", got.Name)
	}
	if got.Spans {
		t.Error("resolved as span-backed — a caller would then render a zero RED row as fact")
	}
	if got.Presence.LogRecords != 1240 {
		t.Errorf("presence = %+v, want the evidence that resolved it", got.Presence)
	}
}

// The stored spelling has to win here too: a LogQuery filters on it literally.
func TestResolveServiceCaseInsensitiveOnALogOnlyName(t *testing.T) {
	s := serverWith(fakeWithLogOnly(fakeWithServices("frontend"), "hotrod"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	got, err := s.resolveService(context.Background(), tr, "HotRod")
	if err != nil || got.Name != "hotrod" {
		t.Fatalf("got %q, %v; want the stored spelling hotrod", got.Name, err)
	}
}

// The probe is a fallback, not a tax. Every resolution that succeeds against
// entry spans — which is nearly all of them — must cost no extra query.
func TestResolveServiceDoesNotProbeOnTheHappyPath(t *testing.T) {
	f := fakeWithLogOnly(fakeWithServices("payment-api"), "hotrod")
	s := serverWith(f)
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	if _, err := s.resolveService(context.Background(), tr, "payment-api"); err != nil {
		t.Fatalf("resolving a span-backed service: %v", err)
	}
	if f.PresenceCalls != 0 {
		t.Errorf("PresenceCalls = %d, want 0 — the happy path must not probe", f.PresenceCalls)
	}
}

// A typo on a log-only name was unsuggestable before this change, because the
// name was not in the list `nearest` searched.
func TestResolveServiceSuggestsALogOnlyName(t *testing.T) {
	s := serverWith(fakeWithLogOnly(fakeWithServices("payment-api"), "hotrod"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	_, err := s.resolveService(context.Background(), tr, "hotrodd")
	var terr *toolError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want a toolError", err)
	}
	if len(terr.DidYouMean) == 0 || terr.DidYouMean[0] != "hotrod" {
		t.Errorf("didYouMean = %v, want hotrod first", terr.DidYouMean)
	}
}

// Honesty in the other direction: an install with the logs module off has not
// checked logs, and must not imply that it did.
func TestResolveServiceSaysWhenLogsCouldNotBeChecked(t *testing.T) {
	f := fakeWithServices("payment-api")
	s := serverWith(f)
	noLogs, err := modules.Parse("core,error-tracking")
	if err != nil {
		t.Fatalf("parsing module set: %v", err)
	}
	s.Modules = noLogs
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	_, err = s.resolveService(context.Background(), tr, "hotrod")
	var terr *toolError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want a toolError", err)
	}
	if !strings.Contains(terr.Message, "logs module is off") {
		t.Errorf("message = %q, want it to admit logs were not checked", terr.Message)
	}
	for _, sig := range f.LastPresenceSignals {
		if sig == storage.SignalLogs {
			t.Error("probed otel_logs on an install where the logs module is off")
		}
	}
}
