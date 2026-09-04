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
		"service, then drill down with search_traces, search_logs or get_trace. On a service mesh, a dependency " +
		"reached through a proxy is reported as the APPLICATION it really is, with viaTransport naming the " +
		"proxies it was recovered across — the proxies themselves are not listed as neighbours.",
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
	// ViaTransport names the proxies a dependency was recovered ACROSS, when
	// the hub reconstructed it by walking a trace's ancestry over a mesh hop.
	// Named as the web client names it (serviceEdgeDTO), so an agent and a
	// person read the same word for the same thing. Absent on a directly
	// observed edge — a reconstructed dependency must never read as one that
	// was seen.
	ViaTransport []string `json:"viaTransport,omitempty"`
	// CollapsedCalls is how much of Calls arrived over those proxies. A pair
	// can talk both ways at once, so this is a portion, not a total.
	CollapsedCalls uint64  `json:"collapsedCalls,omitempty"`
	Calls          uint64  `json:"calls"`
	CallsPerSec    float64 `json:"callsPerSec"`
	ErrorRate      float64 `json:"errorRate"`
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
	cls := s.Topology.WithEvidence(topology.LabelledTransport(all))

	// Recover the app→app dependencies the mesh hides, then drop the hops they
	// were recovered from. Same four steps, same order and the same functions
	// the service map uses: an agent and a person asking what depends on this
	// service must not get different answers.
	//
	// Free on an unmeshed install — `transport` is empty, so the store returns
	// before it queries and HideTransport is a pass-through.
	//
	// `name` is exempt from hiding: this tool describes ONE service, and if that
	// service is itself a proxy, hiding transport would empty the very answer
	// being asked for.
	transport := topology.TransportNames(cls, all)
	collapsed, err := s.Store.CollapsedEdges(ctx, s.serviceQuery(tr), transport)
	if err != nil {
		return nil, fmt.Errorf("recovering mesh-hidden dependencies: %w", err)
	}
	edges = topology.MergeCollapsed(edges, collapsed)
	edges = topology.HideTransport(all, edges, cls, name)

	payload := serviceContextPayload{
		Service:      name,
		Window:       toWindowDTO(tr),
		RED:          toServiceRow(stats, tr),
		Callers:      neighbours(edges, name, tr, inbound),
		Dependencies: neighbours(edges, name, tr, outbound),
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
// The edges arriving here have already been through the mesh collapse
// (design/2026-08-25-transport-hop-collapse.md): an app→proxy→app path is the
// app→app dependency it really is, and the hops are gone. So there is no
// transport counterpart left to label — what a proxy contributed is recorded on
// the dependency itself, as viaTransport.
func neighbours(edges []storage.ServiceEdge, name string, tr storage.TimeRange, dir direction) []neighbourRow {
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
		cur.CollapsedCount += e.CollapsedCount
		cur.ViaTransport = append(cur.ViaTransport, e.ViaTransport...)
		if e.P95 > cur.P95 {
			cur.P95 = e.P95
		}
	}
	out := make([]neighbourRow, 0, len(agg))
	for other, e := range agg {
		row := neighbourRow{
			Service:        other,
			Calls:          e.Count,
			CallsPerSec:    perSec(e.Count, tr),
			ErrorRate:      ratio(e.ErrorCount, e.Count),
			P95Ms:          ms(e.P95),
			ViaTransport:   dedupe(e.ViaTransport),
			CollapsedCalls: e.CollapsedCount,
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

// dedupe returns the distinct names in order of first appearance. A counterpart
// reached over two proxies is summed from two edges, and the same proxy must
// not be named twice in the result.
func dedupe(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	out := names[:0:0]
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
