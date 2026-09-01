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

var searchLogsDef = toolDef{
	Name: "search_logs",
	Description: "Search log records: by service, minimum severity, a substring of the message, or a trace id. " +
		"Passing trace_id returns exactly the logs correlated to that request, which is how you read what one " +
		"failing request actually printed. Log bodies are returned in full.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service":  {Type: "string", Description: "Only logs from this service."},
			"level":    {Type: "string", Description: `Minimum severity, e.g. "WARN" or "ERROR". Matches that level and worse.`},
			"query":    {Type: "string", Description: "Case-insensitive substring of the log body."},
			"trace_id": {Type: "string", Description: "Return the logs correlated to this trace. Ignores the other filters and the window."},
			"limit":    {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type searchLogsArgs struct {
	windowArgs
	Service string `json:"service,omitempty"`
	Level   string `json:"level,omitempty"`
	Query   string `json:"query,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type logRow struct {
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Service   string `json:"service"`
	Body      string `json:"body"`
	TraceID   string `json:"traceId,omitempty"`
	SpanID    string `json:"spanId,omitempty"`
}

func toLogRow(l storage.LogRecord) logRow {
	return logRow{
		Timestamp: l.Timestamp.UTC().Format(time.RFC3339Nano), Severity: l.Severity,
		Service: l.Service, Body: l.Body, TraceID: l.TraceID, SpanID: l.SpanID,
	}
}

type searchLogsPayload struct {
	Window    *windowDTO `json:"window,omitempty"` // absent on a trace_id read, which has no window
	Logs      []logRow   `json:"logs"`
	Returned  int        `json:"returned"`
	Truncated bool       `json:"truncated"`
}

func (p searchLogsPayload) rows() int { return p.Returned }

func runSearchLogs(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a searchLogsArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)

	// A trace id takes the correlated path — a different store call, and the
	// one that answers "what did THIS request print". Mixing it into the
	// search would return whatever else happened to be in the window too.
	if a.TraceID != "" {
		records, err := s.Store.LogsForTrace(ctx, s.Tenants, a.TraceID)
		if err != nil {
			return nil, fmt.Errorf("reading logs for trace: %w", err)
		}
		rows := make([]logRow, 0, len(records))
		for _, l := range records {
			rows = append(rows, toLogRow(l))
		}
		truncated := len(rows) > limit
		if truncated {
			rows = rows[:limit]
		}
		return searchLogsPayload{Logs: rows, Returned: len(rows), Truncated: truncated}, nil
	}

	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	page, err := s.Store.SearchLogs(ctx, storage.LogQuery{
		Tenant:      s.Tenant,
		Tenants:     s.Tenants,
		Range:       tr,
		Service:     service,
		MinSeverity: a.Level,
		Query:       a.Query,
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}
	rows := make([]logRow, 0, len(page.Logs))
	for _, l := range page.Logs {
		rows = append(rows, toLogRow(l))
	}
	w := toWindowDTO(tr)
	return searchLogsPayload{
		Window: &w, Logs: rows,
		Returned: len(rows), Truncated: page.NextCursor != nil,
	}, nil
}

var listErrorIssuesDef = toolDef{
	Name: "list_error_issues",
	Description: "List deduplicated error issues — exceptions grouped by fingerprint, with first/last seen and an " +
		"occurrence count — rather than raw exception events. Unresolved by default. Each issue carries the id of a " +
		"trace it last occurred in, which get_trace will open.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service": {Type: "string", Description: "Only issues from this service."},
			"query":   {Type: "string", Description: "Case-insensitive substring of the exception type or message."},
			"status":  {Type: "string", Description: `Triage state; "unresolved" by default.`, Enum: []string{"unresolved", "resolved", "ignored", "all"}},
			"sort":    {Type: "string", Description: `"lastSeen" (default), "count" or "firstSeen".`, Enum: []string{"lastSeen", "count", "firstSeen"}},
			"limit":   {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type listErrorIssuesArgs struct {
	windowArgs
	Service string `json:"service,omitempty"`
	Query   string `json:"query,omitempty"`
	Status  string `json:"status,omitempty"`
	Sort    string `json:"sort,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type issueRow struct {
	Fingerprint string `json:"fingerprint"`
	Service     string `json:"service"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Source      string `json:"source,omitempty"`
	Status      string `json:"status"`
	Regressed   bool   `json:"regressed,omitempty"`
	Count       uint64 `json:"count"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	LastTraceID string `json:"lastTraceId,omitempty"`
}

func toIssueRow(i storage.ErrorIssue) issueRow {
	return issueRow{
		// Hex, the same wire form the REST API uses (fingerprintHex), so an id
		// an agent reports pastes straight into a URL a human can open.
		Fingerprint: fmt.Sprintf("%016x", i.Fingerprint),
		Service:     i.Service, Type: i.Type, Message: i.Message, Source: i.Source,
		Status: i.Status, Regressed: i.Regressed, Count: i.Count,
		FirstSeen: i.FirstSeen.UTC().Format(time.RFC3339),
		LastSeen:  i.LastSeen.UTC().Format(time.RFC3339),
		// The bridge to get_trace: an issue an agent can only read about is
		// half an answer.
		LastTraceID: i.LastTraceID,
	}
}

type listErrorIssuesPayload struct {
	Window    windowDTO  `json:"window"`
	Issues    []issueRow `json:"issues"`
	Returned  int        `json:"returned"`
	Truncated bool       `json:"truncated"`
}

func (p listErrorIssuesPayload) rows() int { return p.Returned }

func runListErrorIssues(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a listErrorIssuesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	if err := enumOr(a.Status, "status", "unresolved", "resolved", "ignored", "all"); err != nil {
		return nil, err
	}
	if err := enumOr(a.Sort, "sort", "lastSeen", "count", "firstSeen"); err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	status := a.Status
	if status == "" {
		// The REST API's empty status means "every state". This tool defaults
		// to unresolved instead, deliberately and in its description: an agent
		// asking what is broken is not asking what used to be, and a page of
		// long-resolved issues is how an investigation goes down a dead end.
		status = "unresolved"
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)
	issues, err := s.Store.SearchErrorIssues(ctx, storage.ErrorIssueQuery{
		Tenant:  s.Tenant,
		Tenants: s.Tenants,
		Range:   tr,
		Status:  status,
		Service: service,
		Query:   a.Query,
		Sort:    a.Sort,
		// One more than asked for, so "there is more" is a fact and not a
		// guess: this store call returns a slice, not a cursor.
		Limit: limit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("searching error issues: %w", err)
	}
	truncated := len(issues) > limit
	if truncated {
		issues = issues[:limit]
	}
	rows := make([]issueRow, 0, len(issues))
	for _, i := range issues {
		rows = append(rows, toIssueRow(i))
	}
	return listErrorIssuesPayload{
		Window: toWindowDTO(tr), Issues: rows,
		Returned: len(rows), Truncated: truncated,
	}, nil
}
