package api

import (
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/tracestats"
)

// Wire DTOs. Durations are float milliseconds; times are RFC3339 (UTC).

type serviceDTO struct {
	Name       string  `json:"name"`
	SpanCount  uint64  `json:"spanCount"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	// Energy overlay (module green): stamped by the service map only when the
	// green module is active; omitted otherwise so non-green installs keep a
	// byte-identical wire shape.
	Wh    float64 `json:"wh,omitempty"`
	GCO2e float64 `json:"gco2e,omitempty"`
	// Role is "transport" for a mesh proxy, ingress/egress gateway or other
	// infrastructure that carries other services' traffic rather than taking
	// part in it — see internal/topology. Stamped by the service map only, and
	// only when it is NOT the default: an application carries no role field, so
	// a mesh-less install keeps a byte-identical wire shape.
	Role string `json:"role,omitempty"`
	// Namespace is the workload's grouping namespace — k8s.namespace.name, or
	// service.namespace for a pure-SDK app that declares one. Stamped by the
	// service map only, so the map can draw a boundary around a namespace
	// without every services read paying for the label query. Empty when the
	// service declares neither, which the map draws as "no boundary" rather
	// than inventing a bucket.
	Namespace string `json:"namespace,omitempty"`
	// Kind narrows a virtual target (role "virtual") to what it actually is:
	// "database", "cache" or "queue". Empty on every other node — an
	// application is not a kind of anything, it is the default.
	Kind string `json:"kind,omitempty"`
}

type servicesResponse struct {
	Services []serviceDTO `json:"services"`
}

type serviceEdgeDTO struct {
	Source            string  `json:"source"`
	Target            string  `json:"target"`
	Calls             uint64  `json:"calls"`
	ErrorCount        uint64  `json:"errorCount"`
	ErrorRate         float64 `json:"errorRate"`
	Bytes             uint64  `json:"bytes,omitempty"`             // network flow bytes (flow/both edges)
	Provenance        string  `json:"provenance"`                  // "trace", "flow", or "both"
	RTTMs             float64 `json:"rttMs,omitempty"`             // OBI TCP RTT p95 (network-health edges)
	FailedConnections uint64  `json:"failedConnections,omitempty"` // OBI failed/reset TCP connections
	Retransmits       uint64  `json:"retransmits,omitempty"`       // OBI TCP retransmits (packet loss on the link)
	// Client-side latency for this call path. omitempty on purpose: a
	// flow-derived edge has no span to measure, and 0ms would read as "instant"
	// rather than "not measured".
	P50Ms float64 `json:"p50Ms,omitempty"`
	P95Ms float64 `json:"p95Ms,omitempty"`
	// The mesh proxies / gateways this dependency was recovered across
	// (design/2026-08-25-transport-hop-collapse.md). Absent on every directly
	// observed edge, so an unmeshed map's JSON is unchanged.
	ViaTransport []string `json:"viaTransport,omitempty"`
	// The portion of Calls/ErrorCount that crossed a proxy. A client drawing
	// the hops themselves subtracts these to get the directly-observed
	// remainder, so no request is drawn twice.
	CollapsedCalls      uint64 `json:"collapsedCalls,omitempty"`
	CollapsedErrorCount uint64 `json:"collapsedErrorCount,omitempty"`
}

type serviceMapResponse struct {
	Services []serviceDTO     `json:"services"`
	Edges    []serviceEdgeDTO `json:"edges"`
}

type operationDTO struct {
	Service    string  `json:"service"`
	Operation  string  `json:"operation"`
	Count      uint64  `json:"count"`
	ErrorCount uint64  `json:"errorCount"`
	ErrorRate  float64 `json:"errorRate"`
	// Server-side 4xx. omitempty on both, so an operation that refuses
	// nothing serialises exactly as it did before.
	RefusedCount uint64  `json:"refusedCount,omitempty"`
	RefusedRate  float64 `json:"refusedRate,omitempty"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
	P99Ms        float64 `json:"p99Ms"`
}

type overviewResponse struct {
	Operations []operationDTO `json:"operations"`
}

type traceSummaryDTO struct {
	TraceID       string    `json:"traceId"`
	RootService   string    `json:"rootService"`
	RootOperation string    `json:"rootOperation"`
	StartTime     time.Time `json:"startTime"`
	DurationMs    float64   `json:"durationMs"`
	SpanCount     uint64    `json:"spanCount"`
	ErrorCount    uint64    `json:"errorCount"`
	// Server-side 4xx spans, counted apart from errors — a request the server
	// turned away is neither a failure of the service nor a success. omitempty
	// so a trace with none keeps the wire shape it had before.
	RefusedCount uint64 `json:"refusedCount,omitempty"`
	StatusCode   string `json:"statusCode"`
	// The representative span's HTTP status code, absent when it carries none,
	// so the list can show "403" where it used to say "OK".
	HTTPStatus uint16 `json:"httpStatus,omitempty"`
}

type tracesResponse struct {
	Traces     []traceSummaryDTO `json:"traces"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type spanLookupResponse struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
}

type spanEventDTO struct {
	Time       time.Time         `json:"time"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type spanDTO struct {
	SpanID             string            `json:"spanId"`
	ParentSpanID       string            `json:"parentSpanId"`
	Service            string            `json:"service"`
	Operation          string            `json:"operation"`
	Kind               string            `json:"kind"`
	ScopeName          string            `json:"scopeName,omitempty"`
	ScopeVersion       string            `json:"scopeVersion,omitempty"`
	StartTime          time.Time         `json:"startTime"`
	DurationMs         float64           `json:"durationMs"`
	StatusCode         string            `json:"statusCode"`
	StatusMessage      string            `json:"statusMessage,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	ResourceAttributes map[string]string `json:"resourceAttributes,omitempty"`
	Events             []spanEventDTO    `json:"events,omitempty"`
}

type traceResponse struct {
	TraceID    string    `json:"traceId"`
	StartTime  time.Time `json:"startTime"`
	DurationMs float64   `json:"durationMs"`
	Spans      []spanDTO `json:"spans"`
	// Services is where the time went, per service: each span's own duration
	// minus what it spent waiting on its direct children. Carried on the
	// response the trace views already fetch, so the Path view costs no extra
	// request — and so the number it shows is the hub's, not a second
	// implementation of the same arithmetic in the browser.
	Services []traceServiceDTO `json:"services"`
}

// traceServiceDTO mirrors the MCP get_trace tool's rows field-for-field: both
// read hub/internal/tracestats, and an agent and a person should not be given
// different names for the same number.
type traceServiceDTO struct {
	Service    string  `json:"service"`
	SelfTimeMs float64 `json:"selfTimeMs"`
	SpanCount  int     `json:"spanCount"`
	ErrorCount int     `json:"errorCount"`
	// A server 4xx, reported apart from errors rather than folded into them.
	RefusedCount int `json:"refusedCount"`
}

type heatmapCellDTO struct {
	T          int    `json:"t"`
	D          int    `json:"d"`
	Count      uint64 `json:"count"`
	ErrorCount uint64 `json:"errorCount"`
}

type heatmapResponse struct {
	StartTime        time.Time        `json:"startTime"`
	EndTime          time.Time        `json:"endTime"`
	TimeBucketSec    int              `json:"timeBucketSec"`
	DurationBoundsMs []float64        `json:"durationBoundsMs"`
	Cells            []heatmapCellDTO `json:"cells"`
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

func ratio(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func toServiceDTO(s storage.ServiceStats, window time.Duration) serviceDTO {
	secs := window.Seconds()
	if secs <= 0 {
		secs = 1
	}
	return serviceDTO{
		Name:       s.Name,
		SpanCount:  s.SpanCount,
		RatePerSec: float64(s.SpanCount) / secs,
		ErrorRate:  ratio(s.ErrorCount, s.SpanCount),
		P50Ms:      ms(s.P50),
		P95Ms:      ms(s.P95),
		P99Ms:      ms(s.P99),
	}
}

func toServiceEdgeDTO(e storage.ServiceEdge) serviceEdgeDTO {
	return serviceEdgeDTO{
		Source:              e.Source,
		Target:              e.Target,
		Calls:               e.Count,
		ErrorCount:          e.ErrorCount,
		ErrorRate:           ratio(e.ErrorCount, e.Count),
		Bytes:               e.Bytes,
		Provenance:          e.Provenance,
		P50Ms:               ms(e.P50),
		P95Ms:               ms(e.P95),
		ViaTransport:        e.ViaTransport,
		CollapsedCalls:      e.CollapsedCount,
		CollapsedErrorCount: e.CollapsedErrors,
	}
}

func toOperationDTO(o storage.OperationStats) operationDTO {
	return operationDTO{
		Service:      o.Service,
		Operation:    o.Operation,
		Count:        o.Count,
		ErrorCount:   o.ErrorCount,
		ErrorRate:    ratio(o.ErrorCount, o.Count),
		RefusedCount: o.RefusedCount,
		RefusedRate:  ratio(o.RefusedCount, o.Count),
		P50Ms:        ms(o.P50),
		P95Ms:        ms(o.P95),
		P99Ms:        ms(o.P99),
	}
}

func toTraceSummaryDTO(t storage.TraceSummary) traceSummaryDTO {
	return traceSummaryDTO{
		TraceID:       t.TraceID,
		RootService:   t.RootService,
		RootOperation: t.RootOperation,
		StartTime:     t.StartTime.UTC(),
		DurationMs:    ms(t.Duration),
		SpanCount:     t.SpanCount,
		ErrorCount:    t.ErrorCount,
		RefusedCount:  t.RefusedCount,
		StatusCode:    t.StatusCode,
		HTTPStatus:    t.HTTPStatus,
	}
}

func toTraceResponse(t storage.Trace) traceResponse {
	resp := traceResponse{TraceID: t.TraceID, Spans: make([]spanDTO, 0, len(t.Spans))}
	for _, r := range tracestats.SelfTimeByService(t.Spans) {
		resp.Services = append(resp.Services, traceServiceDTO{
			Service:      r.Service,
			SelfTimeMs:   ms(r.SelfTime),
			SpanCount:    r.SpanCount,
			ErrorCount:   r.ErrorCount,
			RefusedCount: r.RefusedCount,
		})
	}
	var end time.Time
	for _, sp := range t.Spans {
		dto := spanDTO{
			SpanID:             sp.SpanID,
			ParentSpanID:       sp.ParentSpanID,
			Service:            sp.Service,
			Operation:          sp.Operation,
			Kind:               sp.Kind,
			ScopeName:          sp.ScopeName,
			ScopeVersion:       sp.ScopeVersion,
			StartTime:          sp.StartTime.UTC(),
			DurationMs:         ms(sp.Duration),
			StatusCode:         sp.StatusCode,
			StatusMessage:      sp.StatusMessage,
			Attributes:         sp.Attributes,
			ResourceAttributes: sp.ResourceAttributes,
		}
		for _, ev := range sp.Events {
			dto.Events = append(dto.Events, spanEventDTO{Time: ev.Time.UTC(), Name: ev.Name, Attributes: ev.Attributes})
		}
		resp.Spans = append(resp.Spans, dto)

		if resp.StartTime.IsZero() || sp.StartTime.Before(resp.StartTime) {
			resp.StartTime = sp.StartTime.UTC()
		}
		if spanEnd := sp.StartTime.Add(sp.Duration); spanEnd.After(end) {
			end = spanEnd
		}
	}
	if !resp.StartTime.IsZero() {
		resp.DurationMs = ms(end.Sub(resp.StartTime))
	}
	return resp
}

// breakdownGroupDTO is one slice of a part-of-whole view.
type breakdownGroupDTO struct {
	// Key is the dimension's value. Empty is meaningful — spans that carry no
	// such attribute — and the UI labels it rather than hiding it.
	Key          string  `json:"key"`
	Count        uint64  `json:"count"`
	ErrorCount   uint64  `json:"errorCount"`
	ErrorRate    float64 `json:"errorRate"`
	RefusedCount uint64  `json:"refusedCount,omitempty"`
	RefusedRate  float64 `json:"refusedRate,omitempty"`
	// DurationMsSum is the group's total wall time — the treemap's other
	// weighting, and the one that answers "where does the time go".
	DurationMsSum float64 `json:"durationMsSum"`
	// Quantiles are absent on the synthetic "other" group: quantiles of a
	// population cannot be recovered by subtracting one sub-population from
	// another, and a plausible wrong number is worse than none.
	P50Ms float64 `json:"p50Ms,omitempty"`
	P95Ms float64 `json:"p95Ms,omitempty"`
	P99Ms float64 `json:"p99Ms,omitempty"`
}

type breakdownResponse struct {
	// GroupBy/Scope echo what was asked, so a cached response is
	// self-describing and a chart can title itself without re-reading the URL.
	GroupBy string              `json:"groupBy"`
	Scope   string              `json:"scope"`
	Groups  []breakdownGroupDTO `json:"groups"`
	// Other aggregates every group past the limit. Present only when the tail
	// is non-empty — and then it MUST be drawn: a part-of-whole chart that
	// silently omits its tail redraws a top-20 as the entire estate.
	Other *breakdownGroupDTO `json:"other,omitempty"`
	// Total covers every matching span, tail included.
	Total breakdownGroupDTO `json:"total"`
	// GroupCount is how many distinct values exist, so a reader can see that
	// the rows are a top-N of something larger.
	GroupCount uint64 `json:"groupCount"`
}

func toBreakdownGroupDTO(g storage.BreakdownGroup) breakdownGroupDTO {
	return breakdownGroupDTO{
		Key:           g.Key,
		Count:         g.Count,
		ErrorCount:    g.ErrorCount,
		ErrorRate:     ratio(g.ErrorCount, g.Count),
		RefusedCount:  g.RefusedCount,
		RefusedRate:   ratio(g.RefusedCount, g.Count),
		DurationMsSum: ms(g.DurationSum),
		P50Ms:         ms(g.P50),
		P95Ms:         ms(g.P95),
		P99Ms:         ms(g.P99),
	}
}

// toBreakdownResponse maps a breakdown and derives the tail bucket.
//
// The tail is arithmetic on the totals rather than a second query: ClickHouse
// already computed the aggregate over every matching span, so what the returned
// groups do not account for IS the tail, exactly.
func toBreakdownResponse(bd storage.Breakdown, groupBy, scope string) breakdownResponse {
	if groupBy == "" {
		groupBy = string(storage.BreakdownService)
	}
	resp := breakdownResponse{
		GroupBy:    groupBy,
		Scope:      scope,
		Groups:     make([]breakdownGroupDTO, 0, len(bd.Groups)),
		Total:      toBreakdownGroupDTO(bd.Total),
		GroupCount: bd.GroupCount,
	}
	var shown storage.BreakdownGroup
	for _, g := range bd.Groups {
		resp.Groups = append(resp.Groups, toBreakdownGroupDTO(g))
		shown.Count += g.Count
		shown.ErrorCount += g.ErrorCount
		shown.RefusedCount += g.RefusedCount
		shown.DurationSum += g.DurationSum
	}
	if bd.Total.Count > shown.Count {
		other := storage.BreakdownGroup{
			Key:          "",
			Count:        bd.Total.Count - shown.Count,
			ErrorCount:   saturatingSub(bd.Total.ErrorCount, shown.ErrorCount),
			RefusedCount: saturatingSub(bd.Total.RefusedCount, shown.RefusedCount),
			DurationSum:  bd.Total.DurationSum - shown.DurationSum,
		}
		dto := toBreakdownGroupDTO(other)
		dto.P50Ms, dto.P95Ms, dto.P99Ms = 0, 0, 0
		resp.Other = &dto
	}
	return resp
}

// saturatingSub keeps a derived count from wrapping around on uint64. The
// totals and the group rows come from the same aggregation pass, so a>=b holds
// in practice; this exists so that a driver or ClickHouse surprise shows up as
// a zero rather than as 18 quintillion errors on a dashboard.
func saturatingSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
