package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// defaultWindow is what a tool reads when the caller names no range: an hour,
// the span of an incident someone is currently inside.
const defaultWindow = time.Hour

// Row bounds. defaultRows is what a model can read without losing the thread;
// maxRows is the most it may ask for. Every payload that hits either says so —
// a top-20 with no marker gets reasoned about as the whole estate, which is
// the same way of being confidently wrong that v0.11's "reported no usage"
// bucket exists to prevent.
const (
	defaultRows = 20
	maxRows     = 100
	maxSpans    = 500
)

// windowArgs is the range vocabulary every tool accepts.
//
// The REST API is absolute-only — parseTimeRange reads RFC3339 start/end and
// nothing else. An agent reasons in "the last twenty minutes", and making it
// compute two timestamps to ask that is friction with no upside. So the
// relative form is translated here, at the tool boundary, and the absolute
// pair stays available for the caller that has one.
type windowArgs struct {
	Window string `json:"window,omitempty"`
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
}

// timeRange resolves the arguments against now. End defaults to now and Start
// to End minus the window, so naming either one alone still yields a range.
func (w windowArgs) timeRange(now time.Time) (storage.TimeRange, error) {
	d := defaultWindow
	if w.Window != "" {
		parsed, err := time.ParseDuration(w.Window)
		if err != nil || parsed <= 0 {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf(
				"window %q is not a positive duration; use a form like 15m, 1h or 24h", w.Window)}
		}
		d = parsed
	}
	end := now
	if w.End != "" {
		t, err := time.Parse(time.RFC3339, w.End)
		if err != nil {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf("end %q is not an RFC3339 timestamp", w.End)}
		}
		end = t
	}
	start := end.Add(-d)
	if w.Start != "" {
		t, err := time.Parse(time.RFC3339, w.Start)
		if err != nil {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf("start %q is not an RFC3339 timestamp", w.Start)}
		}
		start = t
	}
	if !end.After(start) {
		return storage.TimeRange{}, &toolError{Message: "end must be after start"}
	}
	return storage.TimeRange{Start: start.UTC(), End: end.UTC()}, nil
}

// windowDTO is how a payload reports the range it actually read, so a model
// never has to assume which window its numbers came from.
type windowDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func toWindowDTO(tr storage.TimeRange) windowDTO {
	return windowDTO{Start: tr.Start.Format(time.RFC3339), End: tr.End.Format(time.RFC3339)}
}

// clampRows bounds a requested row count: absent (0 or less) takes def,
// anything over max is reduced to it. Silently — what matters is that the
// payload reports what it returned and whether more existed.
func clampRows(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	default:
		return v
	}
}

// serviceQuery is the ServiceQuery every tool builds: the tenant set the API
// resolved, the window, and the same auxiliary-traffic exclusion the screens
// default to — health checks and scrapes are not what anyone is investigating.
func (s *Server) serviceQuery(tr storage.TimeRange) storage.ServiceQuery {
	return storage.ServiceQuery{Tenant: s.Tenant, Tenants: s.Tenants, Range: tr, ExcludeAux: true}
}

// resolvedService is a name this estate knows, and HOW it knows it. The second
// half is not decoration: RED, callers and dependencies are all span-derived,
// so a tool handed a log-only service has to be able to say which of its
// answers are missing for want of spans rather than for want of trouble.
type resolvedService struct {
	// Name is the STORED spelling — it is what every downstream filter matches.
	Name string
	// Stats is the entry-span RED row, valid only when Spans is true. The zero
	// value must never be rendered as RED: "0 req/s, 0% errors" is a claim, and
	// a false one.
	Stats storage.ServiceStats
	// Spans records that the name came from ListServices, i.e. it handled entry
	// spans in this window.
	Spans bool
	// Presence is the signal evidence that resolved a name with no entry spans.
	// Zero when Spans is true — the happy path never probes.
	Presence storage.ServicePresence
}

// resolveService turns the name an agent asked for into one this estate knows.
func (s *Server) resolveService(ctx context.Context, tr storage.TimeRange, name string) (resolvedService, error) {
	res, _, err := s.resolveServiceStats(ctx, tr, name)
	return res, err
}

// resolveServiceStats is resolveService plus the full entry-span service list,
// which service_context needs anyway.
//
// Returning no rows for an unknown name is the cheaper answer and the
// dangerous one: a model handed an empty list for a misspelling reads it as
// "this service is dead" and says so with confidence. Naming the near matches
// turns a dead end into the next question.
//
// The same reasoning is why ListServices alone is not the population to resolve
// against. It counts ENTRY spans, because that is what RED is defined over, so
// a service shipping logs and no server spans — or only client spans, or only
// health checks — is absent from it. Answering "no service named hotrod
// reported anything" while its logs and its open issues are right there is the
// same confident falsehood, told by us instead of inferred by the model. So on
// a miss we widen the evidence rather than the answer: probe the raw signal
// tables, and carry back WHICH signal matched so the caller can be honest.
func (s *Server) resolveServiceStats(ctx context.Context, tr storage.TimeRange, name string) (resolvedService, []storage.ServiceStats, error) {
	if strings.TrimSpace(name) == "" {
		return resolvedService{}, nil, &toolError{Message: "service is required"}
	}
	all, err := s.Store.ListServices(ctx, s.serviceQuery(tr))
	if err != nil {
		return resolvedService{}, nil, fmt.Errorf("listing services: %w", err)
	}
	known := make([]string, 0, len(all))
	for _, svc := range all {
		known = append(known, svc.Name)
	}
	if i, ok := matchName(name, known); ok {
		return resolvedService{Name: all[i].Name, Stats: all[i], Spans: true}, all, nil
	}

	// Miss. Only now does the extra query happen.
	signals := s.presenceSignals()
	present, err := s.Store.ServicePresence(ctx, s.serviceQuery(tr), signals)
	if err != nil {
		return resolvedService{}, all, fmt.Errorf("probing service presence: %w", err)
	}
	others := make([]string, 0, len(present))
	for _, p := range present {
		others = append(others, p.Name)
	}
	if i, ok := matchName(name, others); ok {
		return resolvedService{Name: present[i].Name, Presence: present[i]}, all, nil
	}

	// Genuinely unknown. Say what was actually checked — an install with the
	// logs module off has not checked logs, and claiming otherwise is the same
	// overclaim in the other direction.
	checked := "spans or logs"
	if !s.Modules.Enabled(modules.Logs) {
		checked = "spans (the logs module is off here, so a service reporting only logs could not be checked)"
	}
	return resolvedService{}, all, &toolError{
		Message: fmt.Sprintf("no service named %q reported %s to this project between %s and %s",
			name, checked, tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339)),
		DidYouMean: nearest(name, append(known, others...), 5),
	}
}

// matchName finds want in candidates: exact first, then case-insensitively.
// The stored spelling always wins, because that is what a storage filter
// matches — resolving "HotRod" to "HotRod" would return an empty page.
func matchName(want string, candidates []string) (int, bool) {
	for i, c := range candidates {
		if c == want {
			return i, true
		}
	}
	for i, c := range candidates {
		if strings.EqualFold(c, want) {
			return i, true
		}
	}
	return 0, false
}

// presenceSignals names the tables this install actually has. otel_logs exists
// only when the logs module is on (migration 0002), and probing a table that
// was never created turns "unknown service" into a query error.
func (s *Server) presenceSignals() []storage.Signal {
	signals := []storage.Signal{storage.SignalSpans} // otel_traces is core
	if s.Modules.Enabled(modules.Logs) {
		signals = append(signals, storage.SignalLogs)
	}
	return signals
}

// nearest ranks known names by closeness to want: containment either way first
// (a model that asked for "payment" should be shown "payment-api"), then edit
// distance, budgeted by the name's own length so an unrelated service is never
// suggested.
func nearest(want string, known []string, n int) []string {
	type scored struct {
		name string
		dist int
	}
	lw := strings.ToLower(want)
	budget := len(lw)/3 + 1
	var ranked []scored
	for _, k := range known {
		lk := strings.ToLower(k)
		d := levenshtein(lw, lk)
		switch {
		case strings.Contains(lk, lw) || strings.Contains(lw, lk):
			d = 0
		case d > budget:
			continue
		}
		ranked = append(ranked, scored{k, d})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].dist != ranked[j].dist {
			return ranked[i].dist < ranked[j].dist
		}
		return ranked[i].name < ranked[j].name
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	var names []string
	for _, r := range ranked {
		names = append(names, r.name)
	}
	return names
}

// levenshtein is the classic two-row edit distance — local rather than a
// dependency, because the whole use is ranking a handful of service names.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// ms, ratio and perSec are the three conversions every payload does. Durations
// reach an agent as float milliseconds (the unit the REST API already speaks)
// and counts reach it as a rate or a fraction, never as a raw pair it has to
// divide — a model that divides is a model that can divide wrong.
func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func ratio(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func perSec(count uint64, tr storage.TimeRange) float64 {
	secs := tr.End.Sub(tr.Start).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(count) / secs
}

// noSpansNote states, for a service resolved from something other than entry
// spans, what the trace-shaped answer cannot tell anyone. "It is reporting; it
// is not traced" is the sentence a model needs in order not to conclude an
// outage from an empty list.
func noSpansNote(res resolvedService) string {
	note := fmt.Sprintf("%s reported no entry spans in this window", res.Name)
	switch {
	case res.Presence.LogRecords > 0:
		note += fmt.Sprintf(" but did report %d log records (%d at ERROR or worse)",
			res.Presence.LogRecords, res.Presence.ErrorLogRecords)
	case res.Presence.Spans > 0:
		note += fmt.Sprintf(" but did emit %d client or internal spans", res.Presence.Spans)
	}
	if !res.Presence.LastSeen.IsZero() {
		note += ", last at " + res.Presence.LastSeen.UTC().Format(time.RFC3339)
	}
	return note + ". It is reporting; it is not traced — so an empty trace list " +
		"here is not evidence that it served no requests. Try search_logs."
}
