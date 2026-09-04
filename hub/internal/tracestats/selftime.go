// Package tracestats derives per-service figures from the spans of a single
// trace: where the time went, and how each service's spans actually ended.
//
// It exists because the same arithmetic was written twice — once in the hub for
// the MCP `get_trace` tool, once in the browser for the Path view — and the two
// had already drifted on what counts as an error. One implementation, read by
// both.
package tracestats

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Status is a span's EFFECTIVE outcome, which is not always the status it
// carries. Many SDK auto-instrumentations leave the OTel status Unset even on a
// failing HTTP call, so the raw code alone undercounts errors.
//
//	StatusCode  SpanKind    http status  effective
//	Error       any         any          error   (explicit status always wins)
//	Ok          any         any          ok      (developer-set, final per spec)
//	Unset/''    any         >= 500       error   (5xx errs on SERVER and CLIENT)
//	Unset/''    Client      400..499     error   (CLIENT 4xx is an error)
//	Unset/''    not Client  400..499     refused (the server said no)
//	Unset/''    any         < 400/none   ok
//
// KEEP IN SYNC with ui/src/lib/span-status.ts and errorSpanExpr /
// refusedSpanExpr in hub/internal/storage/clickhouse/status.go. Those two have
// agreed with each other since v0.11 and say so in their own comments; this is
// the third statement of the same rule, and the first in Go.
type Status string

const (
	StatusOK      Status = "ok"
	StatusRefused Status = "refused"
	StatusError   Status = "error"
)

// httpStatus reads both semconv keys and takes the greater, exactly as the SQL
// does with greatest() — a span carrying neither yields 0.
func httpStatus(attrs map[string]string) int {
	parse := func(k string) int {
		n, err := strconv.Atoi(attrs[k])
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	newKey, oldKey := parse("http.response.status_code"), parse("http.status_code")
	if newKey > oldKey {
		return newKey
	}
	return oldKey
}

// EffectiveStatus classifies one span. Error and refused are mutually exclusive
// by construction: an explicit Error, a 5xx and a Client 4xx are all claimed
// first, and an explicit Ok is final per spec.
func EffectiveStatus(sp storage.Span) Status {
	if strings.EqualFold(sp.StatusCode, "error") {
		return StatusError
	}
	if strings.EqualFold(sp.StatusCode, "ok") {
		return StatusOK
	}
	h := httpStatus(sp.Attributes)
	switch {
	case h >= 500, sp.Kind == "Client" && h >= 400:
		return StatusError
	case h >= 400 && h <= 499:
		// A server 4xx. Not an error — the fault is the caller's, and counting
		// it would flood the error rate with auth challenges and crawler 404s —
		// but not a success either.
		return StatusRefused
	default:
		return StatusOK
	}
}

// ServiceSelfTime is one service's contribution to a single trace.
type ServiceSelfTime struct {
	Service string
	// SelfTime is a duration rather than milliseconds: each caller formats at
	// its own DTO boundary, so no rounding is baked in here.
	SelfTime     time.Duration
	SpanCount    int
	ErrorCount   int
	RefusedCount int
}

// SelfTimeByService weights a trace by where the time actually WENT: each
// span's own duration minus what it spent waiting on its DIRECT children,
// rolled up per service. Biggest first, so "where did the time go" is answered
// by the first row.
func SelfTimeByService(spans []storage.Span) []ServiceSelfTime {
	childTime := make(map[string]time.Duration, len(spans))
	for _, sp := range spans {
		if sp.ParentSpanID != "" {
			childTime[sp.ParentSpanID] += sp.Duration
		}
	}
	agg := make(map[string]*ServiceSelfTime, 8)
	for _, sp := range spans {
		self := sp.Duration - childTime[sp.SpanID]
		if self < 0 {
			// Concurrent children can outlast their parent's own clock. Zero,
			// not a negative: "this service waited" is the truth, and a
			// negative would corrupt the rollup it lands in.
			self = 0
		}
		row, ok := agg[sp.Service]
		if !ok {
			row = &ServiceSelfTime{Service: sp.Service}
			agg[sp.Service] = row
		}
		row.SelfTime += self
		row.SpanCount++
		switch EffectiveStatus(sp) {
		case StatusError:
			row.ErrorCount++
		case StatusRefused:
			row.RefusedCount++
		}
	}
	out := make([]ServiceSelfTime, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfTime != out[j].SelfTime {
			return out[i].SelfTime > out[j].SelfTime
		}
		return out[i].Service < out[j].Service
	})
	return out
}
