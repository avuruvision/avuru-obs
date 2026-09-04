package topology

import (
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Edge-set rules for a meshed estate, in one place because more than one client
// needs them and they must not drift.
//
// The service map applied these inside its own HTTP handler, which is why the
// MCP server ended up with a copy of one of them and none of the others: an
// agent asking "what depends on this service" got a proxy where a person
// reading the map got the dependency behind it. These are decisions about what
// a reader is shown, so they live beside the classifier that decides what a
// proxy IS, and every surface calls the same functions.

// LabelledTransport is the subset of `services` the MESH identified, via the
// labels it writes on its own data plane and the sensor carries on their spans
// (design/2026-08-26-transport-from-labels.md).
//
// Derived from the ListServices rows rather than a query of its own, so the
// evidence describes exactly the services under discussion — the two cannot end
// up disagreeing about which window they read.
func LabelledTransport(services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if s.TransportEvidence {
			out = append(out, s.Name)
		}
	}
	return out
}

// TransportNames is the classified proxy/gateway set among these services,
// sorted so the query it feeds binds a stable argument. Passing names (rather
// than the glob patterns) keeps the classification in exactly one place: this
// package decides, SQL only matches.
func TransportNames(cls Classifier, services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if cls.IsTransport(s.Name) {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// MergeCollapsed folds mesh-recovered dependencies into the edge set.
//
// A pair can legitimately have both: some calls routed through the mesh and
// some direct (an exclusion, a port the mesh does not capture). Those are the
// same dependency, so the counts add and `viaTransport` is kept — saying "these
// services do not talk" because part of the traffic took a proxy would be the
// same class of error this feature exists to fix.
//
// Latency comes from whichever path carried more calls. Quantiles over two
// populations cannot be merged after the fact, and the dominant path is the one
// the number actually describes.
func MergeCollapsed(edges, collapsed []storage.ServiceEdge) []storage.ServiceEdge {
	if len(collapsed) == 0 {
		return edges
	}
	type key struct{ src, dst string }
	index := make(map[key]int, len(edges))
	for i, e := range edges {
		index[key{e.Source, e.Target}] = i
	}
	for _, c := range collapsed {
		i, ok := index[key{c.Source, c.Target}]
		if !ok {
			edges = append(edges, c)
			index[key{c.Source, c.Target}] = len(edges) - 1
			continue
		}
		if c.Count > edges[i].Count {
			edges[i].P50, edges[i].P95 = c.P50, c.P95
		}
		edges[i].Count += c.Count
		edges[i].ErrorCount += c.ErrorCount
		edges[i].CollapsedCount += c.CollapsedCount
		edges[i].CollapsedErrors += c.CollapsedErrors
		edges[i].ViaTransport = c.ViaTransport
		// Provenance stays whatever the observed edge already was ("trace",
		// "flow", "both"): the pair IS directly observed, and viaTransport is
		// what records that some of it went the long way.
	}
	return edges
}

// HideTransport drops the mesh hops, leaving the dependencies recovered from
// them — the DOUBLE-COUNT RULE, stated for callers that have no toggle.
//
// `app → proxy → app` and the `app → app` edge MergeCollapsed just folded in
// describe the SAME requests. Showing both counts that traffic twice, so a
// reader has to be given one representation or the other. The service map has a
// toggle and swaps between them (ui/src/lib/map-filter.ts owns the same rule for
// the browser, because the map re-decides it client-side on a cached response).
// A caller without a toggle — an MCP payload, say — takes this default: hops
// hidden, recovered dependencies shown.
//
// `keep` exempts one service from being hidden, for a caller whose subject IS a
// proxy; hiding it would delete the very thing being described. Pass "" for
// none.
//
// Edges are dropped only where an endpoint is a node being HIDDEN — not every
// edge whose endpoint is absent from `services`. The difference matters: an edge
// can legitimately point at something that never sent telemetry, and keeping it
// is what lets a caller report that peer instead of deleting the connection.
func HideTransport(services []storage.ServiceStats, edges []storage.ServiceEdge, cls Classifier, keep string) []storage.ServiceEdge {
	removed := make(map[string]struct{})
	for _, s := range services {
		if s.Name != keep && cls.IsTransport(s.Name) {
			removed[s.Name] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return edges
	}
	out := edges[:0:0]
	for _, e := range edges {
		if _, drop := removed[e.Source]; drop {
			continue
		}
		if _, drop := removed[e.Target]; drop {
			continue
		}
		out = append(out, e)
	}
	return out
}
