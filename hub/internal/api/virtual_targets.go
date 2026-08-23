package api

import (
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// RoleVirtual marks a map node that emits no telemetry of its own: a database,
// cache or message broker seen only through the exit spans of the services
// calling it. It sits beside topology.RoleTransport in the same `role` field —
// both answer "what kind of thing is this node", and both are absent on an
// ordinary application so an install without either keeps its wire shape.
const RoleVirtual = "virtual"

// virtualNodeName is the identity of a virtual target, and it is a URI on
// purpose. The node id has to be stable, readable, and — the load-bearing part —
// incapable of colliding with a service.name, because a collision would merge a
// real service and a database into a single graph node. `postgresql://orders-db`
// cannot be a service name; `orders-db` very much can.
//
// With no peer recorded the node degrades to the bare system, which is still
// true, just less specific.
func virtualNodeName(system, peer string) string {
	if system == "" {
		return ""
	}
	if peer == "" {
		return system
	}
	return system + "://" + peer
}

// virtualNode accumulates one target's rows while they are being folded
// together, so the DTO is built once from finished numbers.
type virtualNode struct {
	name   string
	kind   string
	calls  uint64
	errors uint64
	// p95 is the WORST caller's p95, not a re-aggregation of theirs: quantiles
	// cannot be averaged, and a max at least names a real measurement — "the
	// slowest path into this dependency looked like this".
	p50, p95 time.Duration
}

// appendVirtualTargets folds derived virtual targets into an existing map
// response: one node per (kind, system, peer), one edge per caller, pointing
// into the target for a call the service made and out of it for a message the
// service was delivered.
//
// Returns the extended node and edge slices. A target whose name collides with a
// real service is dropped along with its edges — with URI naming that needs a
// service literally called `redis://cache`, but a duplicate node id would break
// the graph outright, so the guard is cheap insurance rather than a judgement
// call.
func appendVirtualTargets(
	services []serviceDTO,
	edges []serviceEdgeDTO,
	targets []storage.VirtualTarget,
	window time.Duration,
) ([]serviceDTO, []serviceEdgeDTO) {
	if len(targets) == 0 {
		return services, edges
	}
	taken := make(map[string]struct{}, len(services))
	for _, s := range services {
		taken[s.Name] = struct{}{}
	}

	nodes := make(map[string]*virtualNode)
	order := make([]string, 0, len(targets))
	newEdges := make([]serviceEdgeDTO, 0, len(targets))

	for _, t := range targets {
		name := virtualNodeName(t.System, t.Peer)
		if name == "" {
			continue
		}
		if _, clash := taken[name]; clash {
			continue
		}
		n, ok := nodes[name]
		if !ok {
			n = &virtualNode{name: name, kind: t.Kind}
			nodes[name] = n
			order = append(order, name)
		}
		n.calls += t.Count
		n.errors += t.ErrorCount
		if t.P50 > n.p50 {
			n.p50 = t.P50
		}
		if t.P95 > n.p95 {
			n.p95 = t.P95
		}

		// "in" is the consume side of a broker, so the arrow runs out of the
		// target and into the service. Everything else is a call the service
		// made.
		src, dst := t.Service, name
		if t.Direction == "in" {
			src, dst = name, t.Service
		}
		newEdges = append(newEdges, serviceEdgeDTO{
			Source:     src,
			Target:     dst,
			Calls:      t.Count,
			ErrorCount: t.ErrorCount,
			ErrorRate:  ratio(t.ErrorCount, t.Count),
			Provenance: "trace",
			P50Ms:      ms(t.P50),
			P95Ms:      ms(t.P95),
		})
	}

	secs := window.Seconds()
	if secs <= 0 {
		secs = 1
	}
	// Insertion order follows the store's call-volume ordering; sorting by name
	// instead makes the response stable and diffable, which the map does not
	// care about but tests and the JSON output do.
	sort.Strings(order)
	for _, name := range order {
		n := nodes[name]
		services = append(services, serviceDTO{
			Name:       n.name,
			SpanCount:  n.calls,
			RatePerSec: float64(n.calls) / secs,
			ErrorRate:  ratio(n.errors, n.calls),
			P50Ms:      ms(n.p50),
			P95Ms:      ms(n.p95),
			// No p99: the store asks for two quantiles per caller, and
			// inventing a third here would be a number nothing measured.
			Role: RoleVirtual,
			Kind: n.kind,
		})
	}
	return services, append(edges, newEdges...)
}
