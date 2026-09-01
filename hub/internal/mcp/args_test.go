package mcp

import (
	"context"
	"errors"
	"reflect"
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

	if got, err := s.resolveService(context.Background(), tr, "payment-api"); err != nil || got != "payment-api" {
		t.Fatalf("exact match: got %q, %v", got, err)
	}
	// The stored spelling wins, so everything downstream filters on a name the
	// store will actually match.
	if got, err := s.resolveService(context.Background(), tr, "Payment-API"); err != nil || got != "payment-api" {
		t.Fatalf("case-insensitive match: got %q, %v", got, err)
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
