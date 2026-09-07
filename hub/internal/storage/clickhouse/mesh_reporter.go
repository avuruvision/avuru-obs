package clickhouse

import (
	"sort"
	"strconv"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Istio data-plane series, verified against the istio source
// (pkg/telemetry, istio/istio/pkg/config/telemetry and ztunnel/src/metrics).
// Names arrive verbatim from the prometheus receiver, like the control plane's.
const (
	meshRequestsMetric  = "istio_requests_total"               // counter, per request
	meshTCPOpenedMetric = "istio_tcp_connections_opened_total" // counter, per connection

	// connection_security_policy values. The proxy's own vocabulary: anything
	// else it says is counted as unknown, not guessed at.
	meshPolicyMTLS = "mutual_tls"
	meshPolicyNone = "none"
)

// meshTrafficRow is one cell of the data-plane delta query: one metric, as
// reported by one side, for one (caller, destination, policy). N is the
// window's contribution summed over the series in the cell, Series how many
// series contributed — so a cell with series and no rise is still "observed".
type meshTrafficRow struct {
	Metric, Reporter               string
	SrcNS, Src, DstNS, Dst, Policy string
	N, Series                      uint64
	Newest                         time.Time
}

// meshRequestRow is one cell of the per-destination request query: the same
// delta, split by the outcome dimensions the proxy attaches to each request.
type meshRequestRow struct {
	Reporter, SrcNS, Src, Flags, Code, Version string
	N                                          uint64
}

// reporterRank orders the sides that can report one edge. A destination's
// proxy accepted the connection and knows whether it was mTLS; a waypoint
// terminated it on the destination's behalf and knows the same; the caller's
// proxy only knows what it tried to send. Lower is better, and anything the
// product does not recognise ranks last so a known side always wins.
func reporterRank(reporter string) int {
	switch reporter {
	case "destination":
		return 0
	case "waypoint":
		return 1
	case "source":
		return 2
	default:
		return 3
	}
}

type meshDstKey struct{ ns, name string }
type meshEdgeKey struct{ srcNS, src, dstNS, dst string }

// addCounts routes one cell's contribution to the right unit and policy.
func addCounts(c *storage.MeshSecurityCounts, metric, policy string, n uint64) {
	requests := metric == meshRequestsMetric
	switch {
	case policy == meshPolicyMTLS && requests:
		c.MTLSRequests += n
	case policy == meshPolicyMTLS:
		c.MTLSConnections += n
	case policy == meshPolicyNone && requests:
		c.PlaintextRequests += n
	case policy == meshPolicyNone:
		c.PlaintextConnections += n
	case requests:
		c.UnknownRequests += n
	default:
		c.UnknownConnections += n
	}
}

// preferReporter folds the delta cells into per-destination and per-edge
// security, keeping ONE reporting side per destination: both ends of an edge
// report it, and summing them would count every request twice. The best rank
// present for a destination wins, and every cell from the other sides is
// dropped — for that destination's edges too, so the edges add up to the
// workload.
//
// Output order is by name, not by volume: the API sorts as its screen needs,
// and a stable order here keeps the tests honest about content.
func preferReporter(rows []meshTrafficRow) ([]storage.MeshWorkloadSecurity, []storage.MeshEdgeSecurity) {
	best := map[meshDstKey]int{}
	for _, r := range rows {
		k := meshDstKey{r.DstNS, r.Dst}
		if b, ok := best[k]; !ok || reporterRank(r.Reporter) < b {
			best[k] = reporterRank(r.Reporter)
		}
	}

	workloads := map[meshDstKey]*storage.MeshWorkloadSecurity{}
	plaintext := map[meshDstKey]map[meshDstKey]uint64{}
	edges := map[meshEdgeKey]*storage.MeshEdgeSecurity{}
	for _, r := range rows {
		k := meshDstKey{r.DstNS, r.Dst}
		if reporterRank(r.Reporter) != best[k] {
			continue
		}
		w, ok := workloads[k]
		if !ok {
			w = &storage.MeshWorkloadSecurity{Namespace: r.DstNS, Workload: r.Dst, Reporter: r.Reporter}
			workloads[k] = w
			plaintext[k] = map[meshDstKey]uint64{}
		}
		addCounts(&w.Counts, r.Metric, r.Policy, r.N)
		if r.Newest.After(w.LastSeen) {
			w.LastSeen = r.Newest
		}
		if r.Policy == meshPolicyNone && r.N > 0 {
			plaintext[k][meshDstKey{r.SrcNS, r.Src}] += r.N
		}
		ek := meshEdgeKey{r.SrcNS, r.Src, r.DstNS, r.Dst}
		e, ok := edges[ek]
		if !ok {
			e = &storage.MeshEdgeSecurity{
				SourceNamespace: r.SrcNS, Source: r.Src,
				TargetNamespace: r.DstNS, Target: r.Dst, Reporter: r.Reporter,
			}
			edges[ek] = e
		}
		addCounts(&e.Counts, r.Metric, r.Policy, r.N)
	}

	outW := make([]storage.MeshWorkloadSecurity, 0, len(workloads))
	for k, w := range workloads {
		for caller, units := range plaintext[k] {
			w.PlaintextCallers = append(w.PlaintextCallers,
				storage.MeshCaller{Namespace: caller.ns, Workload: caller.name, Units: units})
		}
		sort.Slice(w.PlaintextCallers, func(i, j int) bool {
			a, b := w.PlaintextCallers[i], w.PlaintextCallers[j]
			if a.Units != b.Units {
				return a.Units > b.Units
			}
			return a.Namespace+"/"+a.Workload < b.Namespace+"/"+b.Workload
		})
		outW = append(outW, *w)
	}
	sort.Slice(outW, func(i, j int) bool {
		return outW[i].Namespace+"/"+outW[i].Workload < outW[j].Namespace+"/"+outW[j].Workload
	})

	outE := make([]storage.MeshEdgeSecurity, 0, len(edges))
	for _, e := range edges {
		outE = append(outE, *e)
	}
	sort.Slice(outE, func(i, j int) bool {
		a, b := outE[i], outE[j]
		if a.SourceNamespace+"/"+a.Source != b.SourceNamespace+"/"+b.Source {
			return a.SourceNamespace+"/"+a.Source < b.SourceNamespace+"/"+b.Source
		}
		return a.TargetNamespace+"/"+a.Target < b.TargetNamespace+"/"+b.Target
	})
	return outW, outE
}

// foldRequests applies the same reporter preference to one destination's
// request cells, then counts them by response flag, destination version, and
// caller. A 5xx is the destination's own answer — the proxy records the code
// the upstream returned, or the one it synthesised when the upstream did not
// answer at all (with a response flag saying why).
func foldRequests(rows []meshRequestRow) storage.MeshRequestBreakdown {
	out := storage.MeshRequestBreakdown{
		ResponseFlags:       map[string]uint64{},
		DestinationVersions: map[string]uint64{},
	}
	if len(rows) == 0 {
		return out
	}
	best := reporterRank(rows[0].Reporter)
	for _, r := range rows[1:] {
		if rank := reporterRank(r.Reporter); rank < best {
			best = rank
		}
	}
	out.Measured = true
	callers := map[meshDstKey]*storage.MeshCallerOutcome{}
	for _, r := range rows {
		if reporterRank(r.Reporter) != best {
			continue
		}
		if out.Reporter == "" {
			out.Reporter = r.Reporter
		}
		out.ResponseFlags[r.Flags] += r.N
		out.DestinationVersions[r.Version] += r.N
		k := meshDstKey{r.SrcNS, r.Src}
		c, ok := callers[k]
		if !ok {
			c = &storage.MeshCallerOutcome{Namespace: r.SrcNS, Workload: r.Src}
			callers[k] = c
		}
		c.Requests += r.N
		if code, err := strconv.Atoi(r.Code); err == nil && code >= 500 {
			c.Errors5xx += r.N
		}
	}
	for _, c := range callers {
		out.Callers = append(out.Callers, *c)
	}
	sort.Slice(out.Callers, func(i, j int) bool {
		a, b := out.Callers[i], out.Callers[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Namespace+"/"+a.Workload < b.Namespace+"/"+b.Workload
	})
	return out
}
