// Package checks runs the scheduled HTTP probes declared alongside the service
// -health groups, and records what they found.
//
// Checks answer the one question observed traffic cannot: what happens when
// there IS no traffic. A group with no spans in the freshness window is either
// idle or dead, and the difference is the whole of an on-call night.
//
// The scheduler is inert with nothing configured: no checks, no goroutine, no
// rows, no behaviour change at all. See design/2026-07-20-endpoint-checks.md.
package checks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/health"
)

// Result is one probe's outcome.
type Result struct {
	CheckID string
	Group   string
	At      time.Time
	OK      bool
	Status  int
	Latency time.Duration
	Error   string
	TraceID string
	SpanID  string
	Tenant  string
}

// Recorder persists results. The storage layer implements it; the scheduler
// only needs somewhere to put what it found.
type Recorder interface {
	RecordCheckResult(ctx context.Context, r Result) error
}

// Emitter publishes a span for the probe's own request.
//
// This is the design's hinge: a check is not a side channel, it is synthetic
// traffic, and it appears in RED, on the map and in traces like any other
// client. It is also why the hub becomes an OTLP CLIENT of the gateway rather
// than writing otel_traces itself — see the AEP's span-emission seam. A nil
// Emitter means "no gateway configured": checks still run and still record,
// because the results are the point and the span is the correlation.
type Emitter interface {
	EmitCheckSpan(ctx context.Context, r Result, url string) (traceID, spanID string)
}

// Scheduler runs due checks and records their outcomes.
type Scheduler struct {
	config   func() health.Config
	recorder Recorder
	emitter  Emitter
	client   *http.Client
	tenant   string
	log      *slog.Logger
	now      func() time.Time

	mu   sync.Mutex
	next map[string]time.Time // check id -> when it is next due
}

// New builds a scheduler. `config` is the hot-reloadable service-health config,
// read on every tick so a check added by `kubectl edit` starts running without
// a restart — the same contract the groups themselves already have.
func New(config func() health.Config, rec Recorder, em Emitter, tenant string, log *slog.Logger) *Scheduler {
	return &Scheduler{
		config:   config,
		recorder: rec,
		emitter:  em,
		tenant:   tenant,
		log:      log,
		now:      time.Now,
		next:     map[string]time.Time{},
		// No SSRF guard, deliberately, and unlike the alerting webhooks: a
		// check exists to probe YOUR OWN services, which live on exactly the
		// private and loopback networks that guard blocks. The URLs are
		// admin-authored config, never user input. What we do enforce is a hard
		// timeout and a refusal to follow a redirect to another host.
		client: &http.Client{
			Timeout: health.DefaultCheckTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 0 && req.URL.Host != via[0].URL.Host {
					return fmt.Errorf("check refused a redirect to another host (%s)", req.URL.Host)
				}
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

// Run ticks until ctx is cancelled. It returns immediately when nothing is
// configured at start AND nothing appears later — the tick is cheap and the
// config is hot-reloadable, so the loop stays alive to notice a check being
// added.
func (s *Scheduler) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue executes every check whose next-due time has passed, concurrently, and
// returns when they are all recorded. Exported so a test can drive one round
// without waiting on a ticker.
func (s *Scheduler) RunDue(ctx context.Context) {
	due := s.due()
	if len(due) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, gc := range due {
		wg.Add(1)
		go func(gc health.GroupCheck) {
			defer wg.Done()
			s.runOne(ctx, gc)
		}(gc)
	}
	wg.Wait()
}

// due selects the checks whose interval has elapsed, and reserves their next
// slot. A check whose ID has disappeared from the config is forgotten, so an
// edit that removes one stops it running AND stops it leaking a map entry.
func (s *Scheduler) due() []health.GroupCheck {
	all := s.config().AllChecks()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	live := make(map[string]bool, len(all))
	var due []health.GroupCheck
	for _, gc := range all {
		live[gc.Check.ID] = true
		at, known := s.next[gc.Check.ID]
		if known && now.Before(at) {
			continue
		}
		interval, err := gc.Check.IntervalOrDefault()
		if err != nil {
			// Validate() rejects this at load, so reaching here means the
			// config was replaced by something invalid; skip rather than run at
			// an unbounded rate.
			continue
		}
		s.next[gc.Check.ID] = now.Add(interval)
		due = append(due, gc)
	}
	for id := range s.next {
		if !live[id] {
			delete(s.next, id)
		}
	}
	return due
}

func (s *Scheduler) runOne(ctx context.Context, gc health.GroupCheck) {
	res := s.probe(ctx, gc)

	// The span is emitted BEFORE recording, so the stored row can carry the
	// trace id: a failing check the operator can click straight through to the
	// trace of the failed probe is the join this feature exists for.
	if s.emitter != nil {
		res.TraceID, res.SpanID = s.emitter.EmitCheckSpan(ctx, res, gc.Check.URL)
	}
	if err := s.recorder.RecordCheckResult(ctx, res); err != nil {
		s.log.Warn("check result not recorded", "check", gc.Check.ID, "error", err)
	}
}

// probe performs the request and decides pass or fail.
func (s *Scheduler) probe(ctx context.Context, gc health.GroupCheck) Result {
	res := Result{CheckID: gc.Check.ID, Group: gc.Group, At: s.now(), Tenant: s.tenant}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gc.Check.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "avuru-obs-check/1")

	start := s.now()
	resp, err := s.client.Do(req)
	res.Latency = s.now().Sub(start)
	if err != nil {
		// A dead endpoint and a slow one are both failures, and the message is
		// what tells them apart at 3 a.m.
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode

	switch want := gc.Check.Expect.Status; {
	case want != 0 && resp.StatusCode != want:
		res.Error = fmt.Sprintf("expected status %d, got %d", want, resp.StatusCode)
		return res
	case want == 0 && (resp.StatusCode < 200 || resp.StatusCode > 299):
		res.Error = fmt.Sprintf("expected a 2xx, got %d", resp.StatusCode)
		return res
	}
	if max, err := gc.Check.MaxLatencyOrZero(); err == nil && max > 0 && res.Latency > max {
		// Served, but not within the time anyone would call working.
		res.Error = fmt.Sprintf("responded in %s, over the %s expectation", res.Latency.Round(time.Millisecond), max)
		return res
	}
	res.OK = true
	return res
}
