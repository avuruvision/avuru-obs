package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// maxCollapseDepth is how many consecutive transport spans the ancestry walk
// will step over to find the real caller.
//
// A sidecar mesh interposes two proxy spans on a call (the caller's egress and
// the callee's ingress); Istio ambient interposes up to three (client ztunnel →
// waypoint → server ztunnel). Three is the deepest real topology we know of,
// and every extra level is another self-join over otel_traces on every map
// render — so this is a constant with its rationale, not a config knob an
// operator would have to guess at.
const maxCollapseDepth = 3

// CollapsedEdges recovers application dependencies that a service mesh hides.
//
// On a meshed cluster every call is intercepted, so the traced shape is
// `app → proxy → app`: two edges, neither of them a dependency. v0.8 stopped
// drawing those (design/2026-08-23-service-map-transport.md) and the real edge
// underneath went with them. This walks each trace's own parent chain across up
// to maxCollapseDepth transport spans and reports the `app → app` edge that
// actually happened.
//
// Per-trace ancestry is what makes this safe. Pairing a proxy's inbound edges
// with its outbound ones in aggregate invents an N×M cross-product — the reason
// the parent AEP deferred this. Here every Server span has exactly one parent
// chain, so a call contributes exactly one edge: N callers and M backends
// through one proxy produce at most N+M rows, and only the pairs that occurred.
//
// transport is the classified proxy/gateway set, resolved in Go by
// hub/internal/topology and passed down so the glob logic lives in exactly one
// place. An EMPTY set means there is no mesh: the method returns without
// querying, so an unmeshed install pays nothing.
//
// Reads otel_traces only — core, no module gate, same as ServiceEdges.
func (s *Store) CollapsedEdges(ctx context.Context, q storage.ServiceQuery, transport []string) ([]storage.ServiceEdge, error) {
	if len(transport) == 0 {
		return nil, nil
	}

	var (
		branches []string
		args     []any
	)
	for depth := 1; depth <= maxCollapseDepth; depth++ {
		sql, branchArgs := collapseBranch(depth, q, transport)
		branches = append(branches, sql)
		args = append(args, branchArgs...)
	}

	// One aggregation over the UNIONed raw rows, not one per branch: quantiles
	// cannot be merged after the fact, and the population is the whole set of
	// collapsed calls regardless of how many proxies each crossed.
	query := `
SELECT
    src,
    dst,
    count()                       AS calls,
    countIf(is_error)             AS errors,
    arraySort(groupUniqArrayArray(via)) AS via,
    quantiles(0.5, 0.95)(dur)     AS qs
FROM (
` + strings.Join(branches, "\nUNION ALL\n") + `
)
GROUP BY src, dst
ORDER BY calls DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("collapsed edges: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceEdge
	for rows.Next() {
		var (
			e     storage.ServiceEdge
			quant []float64
		)
		e.Provenance = "collapsed"
		if err := rows.Scan(&e.Source, &e.Target, &e.Count, &e.ErrorCount, &e.ViaTransport, &quant); err != nil {
			return nil, fmt.Errorf("scanning collapsed edge row: %w", err)
		}
		// Two quantiles asked for, three returned; the edge has no p99, exactly
		// as ServiceEdges reports it.
		e.P50, e.P95, _ = nsQuantiles(quant)
		// Everything this query returns crossed a proxy, by construction.
		e.CollapsedCount, e.CollapsedErrors = e.Count, e.ErrorCount
		out = append(out, e)
	}
	return out, rows.Err()
}

// collapseBranch builds the raw-row query for a chain of exactly `depth`
// transport spans between the caller's Client span and the callee's Server
// span, plus its bind arguments in placeholder order.
//
// Branches cannot double-count each other: depth k requires the k-th ancestor
// to be transport and the (k+1)-th NOT to be (it is the client), while depth
// k+1 requires the (k+1)-th to be transport. A span is either in the transport
// set or out of it, so a Server span matches exactly one depth.
func collapseBranch(depth int, q storage.ServiceQuery, transport []string) (string, []any) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)

	// Aliases: the callee's Server span, `depth` transport hops walking up, then
	// the caller's Client span at the top.
	hops := make([]string, depth)
	for i := range hops {
		hops[i] = "h" + strconv.Itoa(i+1)
	}
	chain := append(append([]string{"server"}, hops...), "client")

	var b strings.Builder
	var args []any

	b.WriteString(`SELECT
    client.ServiceName AS src,
    server.ServiceName AS dst,
    ` + errorSpanExpr("server.") + ` AS is_error,
    [` + strings.Join(prefixed(hops, ".ServiceName"), ", ") + `] AS via,
    toFloat64(client.Duration) AS dur
FROM otel_traces AS server`)

	// Walk up the chain: each alias joins to its own parent span.
	for i := 1; i < len(chain); i++ {
		child, parent := chain[i-1], chain[i]
		b.WriteString("\nINNER JOIN otel_traces AS " + parent +
			" ON " + child + ".TraceId = " + parent + ".TraceId" +
			" AND " + child + ".ParentSpanId = " + parent + ".SpanId")
	}

	// Tenant + window on every side of the join, in chain order so the args
	// below stay in lockstep with the placeholders.
	b.WriteString("\nWHERE 1")
	for _, alias := range chain {
		b.WriteString("\n  AND " + alias + ".Tenant IN (?) AND " + alias + ".Timestamp >= ? AND " + alias + ".Timestamp < ?")
		args = append(args, tenants, q.Range.Start, q.Range.End)
	}

	b.WriteString("\n  AND server.SpanKind = 'Server'\n  AND client.SpanKind = 'Client'")

	// Every intermediate span is transport; neither end is.
	for _, h := range hops {
		b.WriteString("\n  AND " + h + ".ServiceName IN (?)")
		args = append(args, transport)
	}
	b.WriteString("\n  AND server.ServiceName NOT IN (?)")
	args = append(args, transport)
	b.WriteString("\n  AND client.ServiceName NOT IN (?)")
	args = append(args, transport)

	// A service that reaches itself through a proxy is not a dependency, the
	// same rule ServiceEdges applies to the direct case.
	b.WriteString("\n  AND server.ServiceName != client.ServiceName")

	if q.ExcludeAux {
		b.WriteString(auxExclusion("server."))
	}
	return b.String(), args
}

func prefixed(names []string, suffix string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + suffix
	}
	return out
}
