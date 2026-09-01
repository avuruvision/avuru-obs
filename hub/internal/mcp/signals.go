package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
