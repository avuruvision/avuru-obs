package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The window properties every tool's schema repeats. Declared once so the
// three of them cannot drift apart across six tools.
func windowProperties() map[string]property {
	return map[string]property{
		"window": {Type: "string", Description: `Relative window ending now, e.g. "15m", "1h", "24h". Default "1h".`},
		"start":  {Type: "string", Description: "Absolute range start (RFC3339). Overrides window."},
		"end":    {Type: "string", Description: "Absolute range end (RFC3339). Defaults to now."},
	}
}

// withWindow returns the window properties plus the tool's own.
func withWindow(own map[string]property) map[string]property {
	out := windowProperties()
	for k, v := range own {
		out[k] = v
	}
	return out
}

var listServicesDef = toolDef{
	Name: "list_services",
	Description: "List the services reporting telemetry in a window, with request rate, error rate and latency percentiles (p50/p95/p99). " +
		"Use it to find a service when you only have a symptom, or to see what is unhealthy right now. Worst first.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"unhealthy_only": {Type: "boolean", Description: "Return only services with a non-zero error rate."},
			"limit":          {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type listServicesArgs struct {
	windowArgs
	UnhealthyOnly bool `json:"unhealthy_only,omitempty"`
	Limit         int  `json:"limit,omitempty"`
}

type serviceRow struct {
	Service    string  `json:"service"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	SpanCount  uint64  `json:"spanCount"`
}

func toServiceRow(s storage.ServiceStats, tr storage.TimeRange) serviceRow {
	return serviceRow{
		Service:    s.Name,
		RatePerSec: perSec(s.SpanCount, tr),
		ErrorRate:  ratio(s.ErrorCount, s.SpanCount),
		P50Ms:      ms(s.P50),
		P95Ms:      ms(s.P95),
		P99Ms:      ms(s.P99),
		SpanCount:  s.SpanCount,
	}
}

type listServicesPayload struct {
	Window    windowDTO    `json:"window"`
	Services  []serviceRow `json:"services"`
	Returned  int          `json:"returned"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated"`
}

func (p listServicesPayload) rows() int { return p.Returned }

func runListServices(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a listServicesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	services, err := s.Store.ListServices(ctx, s.serviceQuery(tr))
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	rows := make([]serviceRow, 0, len(services))
	for _, svc := range services {
		if a.UnhealthyOnly && svc.ErrorCount == 0 {
			continue
		}
		rows = append(rows, toServiceRow(svc, tr))
	}
	// Worst first, busiest next: whatever survives truncation has to be the
	// part worth reading.
	sort.Slice(rows, func(i, j int) bool {
		switch {
		case rows[i].ErrorRate != rows[j].ErrorRate:
			return rows[i].ErrorRate > rows[j].ErrorRate
		case rows[i].RatePerSec != rows[j].RatePerSec:
			return rows[i].RatePerSec > rows[j].RatePerSec
		default:
			return rows[i].Service < rows[j].Service
		}
	})
	total := len(rows)
	limit := clampRows(a.Limit, defaultRows, maxRows)
	truncated := total > limit
	if truncated {
		rows = rows[:limit]
	}
	return listServicesPayload{
		Window: toWindowDTO(tr), Services: rows,
		Returned: len(rows), Total: total, Truncated: truncated,
	}, nil
}

var searchTracesDef = toolDef{
	Name: "search_traces",
	Description: "Find requests matching a filter: service, root operation, status, minimum duration. " +
		"Returns one row per trace with its root operation, duration, span count and error count. " +
		"Use it after service_context to see the actual failing or slow requests, then get_trace on one of them.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service":         {Type: "string", Description: "Only traces touching this service."},
			"operation":       {Type: "string", Description: `Only traces whose root operation matches, e.g. "POST /checkout".`},
			"status":          {Type: "string", Description: "Only failed or only succeeded requests.", Enum: []string{"ok", "error"}},
			"min_duration_ms": {Type: "number", Description: "Only traces at least this slow, in milliseconds."},
			"order":           {Type: "string", Description: `Sort order; "newest" by default.`, Enum: []string{"newest", "oldest", "slowest"}},
			"limit":           {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type searchTracesArgs struct {
	windowArgs
	Service       string  `json:"service,omitempty"`
	Operation     string  `json:"operation,omitempty"`
	Status        string  `json:"status,omitempty"`
	MinDurationMs float64 `json:"min_duration_ms,omitempty"`
	Order         string  `json:"order,omitempty"`
	Limit         int     `json:"limit,omitempty"`
}

type traceRow struct {
	TraceID       string  `json:"traceId"`
	RootService   string  `json:"rootService"`
	RootOperation string  `json:"rootOperation"`
	StartTime     string  `json:"startTime"`
	DurationMs    float64 `json:"durationMs"`
	SpanCount     uint64  `json:"spanCount"`
	ErrorCount    uint64  `json:"errorCount"`
	StatusCode    string  `json:"statusCode,omitempty"`
	HTTPStatus    uint16  `json:"httpStatus,omitempty"`
}

type searchTracesPayload struct {
	Window   windowDTO  `json:"window"`
	Traces   []traceRow `json:"traces"`
	Returned int        `json:"returned"`
	// Truncated follows the store's own cursor: it means "more matched than
	// you are reading", which is the fact that matters. There is deliberately
	// no total — counting the matches would be a query the screens never run,
	// and inventing a read path for this client is the one thing this design
	// refuses to do.
	Truncated bool `json:"truncated"`
}

func (p searchTracesPayload) rows() int { return p.Returned }

// enumOr validates a closed-set argument. An invented value is refused rather
// than ignored, for the reason every unknown argument is: a silently dropped
// filter reads back as a filter that was applied.
func enumOr(value, name string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return &toolError{Message: fmt.Sprintf("%s must be one of %s, got %q", name, strings.Join(allowed, ", "), value)}
}

func runSearchTraces(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a searchTracesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	if err := enumOr(a.Status, "status", "ok", "error"); err != nil {
		return nil, err
	}
	if err := enumOr(a.Order, "order", "newest", "oldest", "slowest"); err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)
	page, err := s.Store.SearchTraces(ctx, storage.TraceQuery{
		Tenant:      s.Tenant,
		Tenants:     s.Tenants,
		Range:       tr,
		Service:     service,
		Operation:   a.Operation,
		Status:      a.Status,
		Order:       a.Order,
		MinDuration: time.Duration(a.MinDurationMs * float64(time.Millisecond)),
		ExcludeAux:  true,
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching traces: %w", err)
	}
	rows := make([]traceRow, 0, len(page.Traces))
	for _, t := range page.Traces {
		rows = append(rows, traceRow{
			TraceID: t.TraceID, RootService: t.RootService, RootOperation: t.RootOperation,
			StartTime: t.StartTime.UTC().Format(time.RFC3339), DurationMs: ms(t.Duration),
			SpanCount: t.SpanCount, ErrorCount: t.ErrorCount,
			StatusCode: t.StatusCode, HTTPStatus: t.HTTPStatus,
		})
	}
	return searchTracesPayload{
		Window: toWindowDTO(tr), Traces: rows,
		Returned: len(rows), Truncated: page.NextCursor != nil,
	}, nil
}

var getTraceDef = toolDef{
	Name: "get_trace",
	Description: "Fetch one trace by id: its spans (id, parent, service, operation, kind, duration, status) " +
		"plus a per-service rollup of SELF time — the time actually spent inside each service rather than " +
		"waiting on something it called. Use it to find which hop in a request is the slow or failing one.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: map[string]property{
			"trace_id": {Type: "string", Description: "The trace id, as returned by search_traces."},
			"limit":    {Type: "integer", Description: "Maximum spans (default and maximum 500)."},
		},
		Required: []string{"trace_id"},
	},
}

type getTraceArgs struct {
	TraceID string `json:"trace_id"`
	Limit   int    `json:"limit,omitempty"`
}

type spanRow struct {
	SpanID        string  `json:"spanId"`
	ParentSpanID  string  `json:"parentSpanId,omitempty"`
	Service       string  `json:"service"`
	Operation     string  `json:"operation"`
	Kind          string  `json:"kind,omitempty"`
	StartTime     string  `json:"startTime"`
	DurationMs    float64 `json:"durationMs"`
	StatusCode    string  `json:"statusCode,omitempty"`
	StatusMessage string  `json:"statusMessage,omitempty"`
}

type serviceSelfTime struct {
	Service    string  `json:"service"`
	SelfTimeMs float64 `json:"selfTimeMs"`
	SpanCount  int     `json:"spanCount"`
	ErrorCount int     `json:"errorCount"`
}

type getTracePayload struct {
	TraceID   string            `json:"traceId"`
	Spans     []spanRow         `json:"spans"`
	Services  []serviceSelfTime `json:"services"`
	Returned  int               `json:"returned"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

func (p getTracePayload) rows() int { return p.Returned }

func runGetTrace(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a getTraceArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.TraceID) == "" {
		return nil, &toolError{Message: "trace_id is required"}
	}
	trace, err := s.Store.GetTrace(ctx, s.Tenants, a.TraceID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, &toolError{Message: fmt.Sprintf(
			"no trace %q in this project — it may have aged out of retention, or belong to another project", a.TraceID)}
	}
	if err != nil {
		return nil, fmt.Errorf("reading trace: %w", err)
	}

	// The rollup is computed over EVERY span, before the page is cut. Bounding
	// what an agent reads must not quietly change what the totals mean.
	services := selfTimeByService(trace.Spans)

	spans := make([]spanRow, 0, len(trace.Spans))
	for _, sp := range trace.Spans {
		spans = append(spans, spanRow{
			SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID,
			Service: sp.Service, Operation: sp.Operation, Kind: sp.Kind,
			StartTime: sp.StartTime.UTC().Format(time.RFC3339Nano), DurationMs: ms(sp.Duration),
			StatusCode: sp.StatusCode, StatusMessage: sp.StatusMessage,
		})
	}
	// Chronological, so a truncated page keeps the beginning of the request —
	// the root and its first hops, which is where an investigation starts.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].StartTime != spans[j].StartTime {
			return spans[i].StartTime < spans[j].StartTime
		}
		return spans[i].SpanID < spans[j].SpanID
	})
	total := len(spans)
	limit := clampRows(a.Limit, maxSpans, maxSpans)
	truncated := total > limit
	if truncated {
		spans = spans[:limit]
	}
	return getTracePayload{
		TraceID: trace.TraceID, Spans: spans, Services: services,
		Returned: len(spans), Total: total, Truncated: truncated,
	}, nil
}

// selfTimeByService weights a trace by where the time actually WENT: each
// span's own duration minus what it spent waiting on its direct children,
// rolled up per service.
//
// This arithmetic already exists once, in the browser
// (ui/src/components/traces/views/trace-path.tsx), where the Path view
// computes it client-side — so the hub has nothing to hand over. Computing it
// here rather than moving the UI's copy is a deliberate cost: unifying them
// means touching a shipped screen, which would put a UI regression in the
// blast radius of a change that otherwise adds nothing to any existing
// surface. The follow-up — the screen reading this number instead — is a
// separate and much safer change.
func selfTimeByService(spans []storage.Span) []serviceSelfTime {
	childTime := make(map[string]time.Duration, len(spans))
	for _, sp := range spans {
		if sp.ParentSpanID != "" {
			childTime[sp.ParentSpanID] += sp.Duration
		}
	}
	agg := make(map[string]*serviceSelfTime, 8)
	for _, sp := range spans {
		self := sp.Duration - childTime[sp.SpanID]
		if self < 0 {
			// Concurrent children can outlast their parent's own clock. Zero,
			// not a negative: "this service waited" is the truth, and a
			// negative number would corrupt the rollup it lands in.
			self = 0
		}
		row, ok := agg[sp.Service]
		if !ok {
			row = &serviceSelfTime{Service: sp.Service}
			agg[sp.Service] = row
		}
		row.SelfTimeMs += ms(self)
		row.SpanCount++
		if strings.EqualFold(sp.StatusCode, "error") {
			row.ErrorCount++
		}
	}
	out := make([]serviceSelfTime, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	// Biggest first: "where did the time go" is answered by the first row.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfTimeMs != out[j].SelfTimeMs {
			return out[i].SelfTimeMs > out[j].SelfTimeMs
		}
		return out[i].Service < out[j].Service
	})
	return out
}
