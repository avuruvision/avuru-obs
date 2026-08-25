package api

import (
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
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
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
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
	StatusCode    string    `json:"statusCode"`
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
		Source:     e.Source,
		Target:     e.Target,
		Calls:      e.Count,
		ErrorCount: e.ErrorCount,
		ErrorRate:  ratio(e.ErrorCount, e.Count),
		Bytes:      e.Bytes,
		Provenance: e.Provenance,
		P50Ms:      ms(e.P50),
		P95Ms:      ms(e.P95),
	}
}

func toOperationDTO(o storage.OperationStats) operationDTO {
	return operationDTO{
		Service:    o.Service,
		Operation:  o.Operation,
		Count:      o.Count,
		ErrorCount: o.ErrorCount,
		ErrorRate:  ratio(o.ErrorCount, o.Count),
		P50Ms:      ms(o.P50),
		P95Ms:      ms(o.P95),
		P99Ms:      ms(o.P99),
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
		StatusCode:    t.StatusCode,
	}
}

func toTraceResponse(t storage.Trace) traceResponse {
	resp := traceResponse{TraceID: t.TraceID, Spans: make([]spanDTO, 0, len(t.Spans))}
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
