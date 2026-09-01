package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

var serviceContextDef = toolDef{
	Name: "service_context",
	Description: "Everything known about one service in a window, in a single call: request rate, error rate and " +
		"latency percentiles; who calls it and what it depends on, each with call rate, error rate and client-side " +
		"p95; its open error issues; and any alerts firing in this project. START HERE when investigating a " +
		"service, then drill down with search_traces, search_logs or get_trace.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service": {Type: "string", Description: "The service to describe."},
		}),
		Required: []string{"service"},
	},
}

type serviceContextArgs struct {
	windowArgs
	Service string `json:"service"`
}

type neighbourRow struct {
	Service string `json:"service"`
	// Role is "transport" for a mesh proxy or gateway, absent for an
	// application. A proxy is reported rather than hidden: on a meshed install
	// dropping it would leave an empty list, which reads as "this service
	// depends on nothing".
	Role        string  `json:"role,omitempty"`
	Calls       uint64  `json:"calls"`
	CallsPerSec float64 `json:"callsPerSec"`
	ErrorRate   float64 `json:"errorRate"`
	// Client-side p95: what the CALLER experienced on this path, which is what
	// makes a single slow route into an otherwise healthy service visible.
	P95Ms float64 `json:"p95Ms,omitempty"`
}

type alertRow struct {
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Since  string `json:"since"`
}

type serviceContextPayload struct {
	Service      string         `json:"service"`
	Window       windowDTO      `json:"window"`
	RED          serviceRow     `json:"red"`
	Callers      []neighbourRow `json:"callers"`
	Dependencies []neighbourRow `json:"dependencies"`
	// Absent — not empty — when the module that owns them is off. An empty
	// list is a claim ("no issues"); absence plus a note is the truth.
	TopIssues    []issueRow `json:"topIssues,omitempty"`
	FiringAlerts []alertRow `json:"firingAlerts,omitempty"`
	// Notes names what this install could not answer and why. Without it a
	// model reads a missing section as an absence of trouble, which is the
	// same way of being confidently wrong that v0.11's "reported no usage"
	// bucket exists to prevent.
	Notes    []string `json:"notes,omitempty"`
	Returned int      `json:"returned"`
}

func (p serviceContextPayload) rows() int { return p.Returned }

func runServiceContext(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a serviceContextArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	stats, all, err := s.resolveServiceStats(ctx, tr, a.Service)
	if err != nil {
		return nil, err
	}
	name := stats.Name

	edges, err := s.Store.ServiceEdges(ctx, s.serviceQuery(tr))
	if err != nil {
		return nil, fmt.Errorf("reading service edges: %w", err)
	}
	// One classifier for the whole answer, carrying the mesh's own label
	// evidence, exactly as the service map builds it — so the two cannot end
	// up disagreeing about which workload is a proxy.
	cls := s.Topology.WithEvidence(labelledTransport(all))

	payload := serviceContextPayload{
		Service:      name,
		Window:       toWindowDTO(tr),
		RED:          toServiceRow(stats, tr),
		Callers:      neighbours(edges, name, tr, cls, inbound),
		Dependencies: neighbours(edges, name, tr, cls, outbound),
	}

	if s.Modules.Enabled(modules.ErrorTracking) {
		issues, err := s.Store.SearchErrorIssues(ctx, storage.ErrorIssueQuery{
			Tenant: s.Tenant, Tenants: s.Tenants, Range: tr,
			Status: "unresolved", Service: name, Sort: "count", Limit: 5,
		})
		if err != nil {
			return nil, fmt.Errorf("reading error issues: %w", err)
		}
		payload.TopIssues = make([]issueRow, 0, len(issues))
		for _, i := range issues {
			payload.TopIssues = append(payload.TopIssues, toIssueRow(i))
		}
	} else {
		payload.Notes = append(payload.Notes,
			"the error-tracking module is not enabled on this install, so topIssues is absent — that is not the same as this service having no errors")
	}

	if s.Modules.Enabled(modules.Alerting) {
		alerts, err := s.firingAlerts(ctx)
		if err != nil {
			return nil, err
		}
		payload.FiringAlerts = alerts
	} else {
		payload.Notes = append(payload.Notes,
			"the alerting module is not enabled on this install, so firingAlerts is absent — nothing here says whether anyone was paged")
	}

	if !s.Modules.Enabled(modules.Logs) {
		payload.Notes = append(payload.Notes,
			"the logs module is not enabled on this install, so search_logs is unavailable")
	}

	payload.Returned = len(payload.Callers) + len(payload.Dependencies) + len(payload.TopIssues) + len(payload.FiringAlerts)
	return payload, nil
}

// firingAlerts returns every alert currently firing in this project, not just
// the ones whose target is this service by name.
//
// A rule targets a service OR a service-health group, so filtering by name
// would silently hide the alert that actually covers this service — the one
// piece of context an investigation most wants. Firing alerts are rare, the
// set is small, and every row carries its target, so the caller can tell.
func (s *Server) firingAlerts(ctx context.Context) ([]alertRow, error) {
	var out []alertRow
	for _, tenant := range s.Tenants {
		states, err := s.Store.LoadAlertStates(ctx, tenant)
		if err != nil {
			return nil, fmt.Errorf("reading alert state: %w", err)
		}
		for _, st := range states {
			if st.Status != "firing" {
				continue
			}
			out = append(out, alertRow{
				Rule: st.RuleName, Target: st.Target,
				Since: st.Since.UTC().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since // longest-firing first
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

// direction selects which end of an edge is the counterpart.
type direction int

const (
	inbound  direction = iota // edges INTO the service: its callers
	outbound                  // edges OUT of it: its dependencies
)

// neighbours builds one side of the dependency picture, summing the edges that
// touch `name` and reporting each counterpart once.
//
// Mesh-hidden hops are NOT recovered here: the service map collapses an
// app→proxy→app path back into the app→app dependency it really is
// (design/2026-08-25-transport-hop-collapse.md), and reimplementing that merge
// for this client would be a second set of semantics to keep in step with the
// first. What this does instead is LABEL a transport counterpart, so an agent
// reading "istio-ingressgateway (transport)" knows it is looking at a hop
// rather than at an application. Bringing the collapse itself here is a
// follow-up, and a safe one.
func neighbours(edges []storage.ServiceEdge, name string, tr storage.TimeRange, cls topology.Classifier, dir direction) []neighbourRow {
	agg := map[string]*storage.ServiceEdge{}
	for i := range edges {
		e := edges[i]
		var other string
		switch dir {
		case inbound:
			if e.Target != name {
				continue
			}
			other = e.Source
		case outbound:
			if e.Source != name {
				continue
			}
			other = e.Target
		}
		if other == "" || other == name {
			continue
		}
		cur, ok := agg[other]
		if !ok {
			cp := e
			agg[other] = &cp
			continue
		}
		cur.Count += e.Count
		cur.ErrorCount += e.ErrorCount
		if e.P95 > cur.P95 {
			cur.P95 = e.P95
		}
	}
	out := make([]neighbourRow, 0, len(agg))
	for other, e := range agg {
		row := neighbourRow{
			Service:     other,
			Calls:       e.Count,
			CallsPerSec: perSec(e.Count, tr),
			ErrorRate:   ratio(e.ErrorCount, e.Count),
			P95Ms:       ms(e.P95),
		}
		if cls.IsTransport(other) {
			row.Role = string(topology.RoleTransport)
		}
		out = append(out, row)
	}
	// Worst first, busiest next — the same order list_services uses, so a
	// reader learns one rule and it holds everywhere.
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].ErrorRate != out[j].ErrorRate:
			return out[i].ErrorRate > out[j].ErrorRate
		case out[i].Calls != out[j].Calls:
			return out[i].Calls > out[j].Calls
		default:
			return out[i].Service < out[j].Service
		}
	})
	return out
}

// labelledTransport is the subset of services the MESH ITSELF identified, via
// the labels it writes on its own data plane and the sensor carries on their
// spans. Derived from the same rows this answer was built from, so the
// evidence cannot describe a different set of services than the answer does.
func labelledTransport(services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if s.TransportEvidence {
			out = append(out, s.Name)
		}
	}
	return out
}
