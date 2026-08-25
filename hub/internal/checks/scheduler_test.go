package checks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

type recorder struct {
	mu      sync.Mutex
	results []Result
}

func (r *recorder) RecordCheckResult(_ context.Context, res Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
	return nil
}

func (r *recorder) all() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.results...)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func configWith(checks ...health.Check) func() health.Config {
	cfg := health.Config{
		DefaultTier: health.TierT2,
		Groups: []health.Group{{
			Name: "core", Tier: health.TierT0,
			Selector: health.Selector{Services: []string{"api"}},
			Checks:   checks,
		}},
	}
	return func() health.Config { return cfg }
}

func TestProbePassesOnHealthyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := &recorder{}
	s := New(configWith(health.Check{ID: "core", URL: srv.URL}), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())

	got := rec.all()
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("want one passing result, got %+v", got)
	}
	if got[0].Group != "core" || got[0].Tenant != "default" {
		t.Errorf("result not attributed to its group/tenant: %+v", got[0])
	}
}

// A dead endpoint is the case the feature exists for, and the message is what
// distinguishes it from a slow one at 3 a.m.
func TestProbeFailsOnDeadEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more

	rec := &recorder{}
	s := New(configWith(health.Check{ID: "core", URL: url}), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())

	got := rec.all()
	if len(got) != 1 || got[0].OK {
		t.Fatalf("want one failing result, got %+v", got)
	}
	if got[0].Error == "" {
		t.Error("a failed probe recorded no error — the message is the actionable part")
	}
}

// Serving, but not in a time anyone would call working.
func TestProbeFailsOnLatencyExpectation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := &recorder{}
	s := New(configWith(health.Check{
		ID: "core", URL: srv.URL, Expect: health.Expect{MaxLatency: "10ms"},
	}), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())

	got := rec.all()
	if len(got) != 1 || got[0].OK {
		t.Fatalf("a slow 200 passed: %+v", got)
	}
	if got[0].Status != http.StatusOK {
		t.Errorf("status = %d, want the 200 it actually returned", got[0].Status)
	}
}

func TestProbeHonoursExpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rec := &recorder{}
	s := New(configWith(health.Check{
		ID: "core", URL: srv.URL, Expect: health.Expect{Status: http.StatusNoContent},
	}), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())
	if got := rec.all(); len(got) != 1 || !got[0].OK {
		t.Fatalf("204 rejected despite being expected: %+v", got)
	}
}

// The scheduler must respect the interval, or a 60s check becomes a load test.
func TestDueRespectsTheInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := &recorder{}
	s := New(configWith(health.Check{ID: "core", URL: srv.URL, Interval: "1m"}), rec, nil, "default", quietLogger())

	now := time.Now()
	s.now = func() time.Time { return now }
	s.RunDue(context.Background())
	s.RunDue(context.Background()) // same instant: not due again
	if n := len(rec.all()); n != 1 {
		t.Fatalf("ran %d times at one instant, want 1", n)
	}

	now = now.Add(61 * time.Second)
	s.RunDue(context.Background())
	if n := len(rec.all()); n != 2 {
		t.Fatalf("ran %d times after the interval elapsed, want 2", n)
	}
}

// Nothing configured must mean nothing happens — no goroutine, no rows, no
// behaviour change on an install that declares no checks.
func TestNoChecksDoesNothing(t *testing.T) {
	rec := &recorder{}
	s := New(configWith(), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())
	if n := len(rec.all()); n != 0 {
		t.Fatalf("recorded %d results with no checks configured", n)
	}
}

// A check removed by a `kubectl edit` must stop running — and stop occupying a
// slot in the scheduler's memory.
func TestRemovedCheckIsForgotten(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := health.Config{DefaultTier: health.TierT2, Groups: []health.Group{{
		Name: "core", Tier: health.TierT0,
		Selector: health.Selector{Services: []string{"api"}},
		Checks:   []health.Check{{ID: "core", URL: srv.URL}},
	}}}
	rec := &recorder{}
	s := New(func() health.Config { return cfg }, rec, nil, "default", quietLogger())
	s.RunDue(context.Background())

	cfg.Groups[0].Checks = nil // the operator removed it
	s.RunDue(context.Background())

	if n := len(rec.all()); n != 1 {
		t.Fatalf("a removed check ran again (%d results)", n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.next) != 0 {
		t.Errorf("removed check left %d scheduling entries behind", len(s.next))
	}
}

// A redirect off-host is how a probe silently starts measuring something else.
func TestProbeRefusesCrossHostRedirect(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer srv.Close()

	rec := &recorder{}
	s := New(configWith(health.Check{ID: "core", URL: srv.URL}), rec, nil, "default", quietLogger())
	s.RunDue(context.Background())

	if got := rec.all(); len(got) != 1 || got[0].OK {
		t.Fatalf("a probe followed a redirect to another host: %+v", got)
	}
}
