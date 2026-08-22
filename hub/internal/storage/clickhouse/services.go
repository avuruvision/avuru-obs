package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ListServices aggregates RED stats per service over entry spans
// (SpanKind Server/Consumer — the request-handling side).
func (s *Store) ListServices(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceStats, error) {
	query := `
SELECT
    ServiceName,
    count()                                         AS spans,
    countIf(` + errorSpanExpr("") + `)              AS errors,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?
  AND SpanKind IN ('Server', 'Consumer')`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
GROUP BY ServiceName
ORDER BY spans DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceStats
	for rows.Next() {
		var (
			st    storage.ServiceStats
			quant []float64
		)
		if err := rows.Scan(&st.Name, &st.SpanCount, &st.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning service row: %w", err)
		}
		st.P50, st.P95, st.P99 = nsQuantiles(quant)
		out = append(out, st)
	}
	return out, rows.Err()
}

// ServiceEdges derives caller→callee call edges from trace spans: a Server span
// whose parent is a Client span in a different service. It self-joins otel_traces
// on (TraceId, ParentSpanId=SpanId). Acceptable at staging volumes; revisit with
// a materialized rollup if otel_traces grows large.
func (s *Store) ServiceEdges(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	query := `
SELECT
    client.ServiceName                   AS src,
    server.ServiceName                   AS dst,
    count()                              AS calls,
    countIf(` + errorSpanExpr("server.") + `) AS errors,
    quantiles(0.5, 0.95)(toFloat64(client.Duration)) AS qs
FROM otel_traces AS server
INNER JOIN otel_traces AS client
    ON server.TraceId = client.TraceId AND server.ParentSpanId = client.SpanId
WHERE server.Tenant IN (?)
  AND server.Timestamp >= ? AND server.Timestamp < ?
  AND client.Tenant IN (?)
  AND client.Timestamp >= ? AND client.Timestamp < ?
  AND server.SpanKind = 'Server' AND client.SpanKind = 'Client'
  AND server.ServiceName != client.ServiceName`
	// Both join sides take the full set: a trace crossing two member tenants
	// yields a cross-tenant edge, which is the merged-project semantics.
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	args := []any{tenants, q.Range.Start, q.Range.End, tenants, q.Range.Start, q.Range.End}
	if q.ExcludeAux {
		query += auxExclusion("server.")
	}
	query += `
GROUP BY src, dst
ORDER BY calls DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service edges: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceEdge
	for rows.Next() {
		var (
			e     storage.ServiceEdge
			quant []float64
		)
		if err := rows.Scan(&e.Source, &e.Target, &e.Count, &e.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning edge row: %w", err)
		}
		// nsQuantiles returns three; this query asks for two, and the third
		// comes back zero — the edge has no p99.
		e.P50, e.P95, _ = nsQuantiles(quant)
		out = append(out, e)
	}
	return out, rows.Err()
}

// networkFlowMetric is the OBI-exported OTLP metric carrying per-flow byte
// counts, tagged with the k8s src/dst owner (workload) names OBI resolves —
// which we use as the map edge's endpoints.
const networkFlowMetric = "obi.network.flow.bytes"

// NetworkEdges derives service→service edges from OBI network flow metrics in
// otel_metrics_sum, so services that emit NO application traces still show up on
// the map. Endpoints are the k8s owner (workload) names OBI tags each flow with;
// self-edges and unlabeled flows are dropped.
//
// sum(Value) over what is a cumulative Sum counter is an APPROXIMATION: it does
// not reconstruct a true byte-rate (that would need a per-series max()-min()
// delta, as ListNodeStats' network rates do). It's used here only for edge
// PRESENCE and relative weighting at eval scale; revisit with a per-series delta
// rollup if an exact byte-rate is ever needed — the same "revisit with a rollup"
// caveat as ServiceEdges' self-join.
//
// This reads the otel_metrics_* tables, which exist only when the infra-metrics
// module is active; callers must gate accordingly.
func (s *Store) NetworkEdges(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error) {
	// Flow metrics carry no span attributes, so q.ExcludeAux has nothing to
	// filter here and is deliberately ignored.
	const query = `
SELECT
    Attributes['k8s.src.owner.name'] AS src,
    Attributes['k8s.dst.owner.name'] AS dst,
    toUInt64(sum(Value))             AS bytes
FROM otel_metrics_sum
WHERE Tenant IN (?)
  AND MetricName = ?
  AND TimeUnix >= ? AND TimeUnix < ?
  AND Attributes['k8s.src.owner.name'] != ''
  AND Attributes['k8s.dst.owner.name'] != ''
  AND Attributes['k8s.src.owner.name'] != Attributes['k8s.dst.owner.name']
GROUP BY src, dst
ORDER BY bytes DESC`

	rows, err := s.conn.Query(ctx, query, tenantsOrDefault(q.Tenants, q.Tenant), networkFlowMetric, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("network edges: %w", err)
	}
	defer rows.Close()

	var out []storage.ServiceEdge
	for rows.Next() {
		// Count/ErrorCount stay 0 — flows carry byte volume, not call volume.
		e := storage.ServiceEdge{Provenance: "flow"}
		if err := rows.Scan(&e.Source, &e.Target, &e.Bytes); err != nil {
			return nil, fmt.Errorf("scanning network edge row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TraceOverview aggregates RED stats per (service, operation) over entry
// spans (Server/Consumer) — the same population ListServices and REDSeries
// count, so the numbers stay comparable across screens. Entry spans (rather
// than parentless roots) mean a downstream service's operations show
// up both when filtering by that service and in the unfiltered per-service
// grouping, and orphaned/partial traces' operations aren't invisible (same
// concern SearchTraces addresses with its effective root). Each request is
// counted once per service that handled it — the standard RED definition.
//
// Ordered by service name first (stable server-side grouping for the UI),
// busiest operations first within a service.
func (s *Store) TraceOverview(ctx context.Context, q storage.OverviewQuery) ([]storage.OperationStats, error) {
	query := `
SELECT
    ServiceName,
    SpanName,
    count()                                         AS reqs,
    countIf(` + errorSpanExpr("") + `)              AS errors,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?
  AND SpanKind IN ('Server', 'Consumer')`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	if q.Service != "" {
		query += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
GROUP BY ServiceName, SpanName
ORDER BY ServiceName ASC, reqs DESC`

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("trace overview: %w", err)
	}
	defer rows.Close()

	var out []storage.OperationStats
	for rows.Next() {
		var (
			op    storage.OperationStats
			quant []float64
		)
		if err := rows.Scan(&op.Service, &op.Operation, &op.Count, &op.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning overview row: %w", err)
		}
		op.P50, op.P95, op.P99 = nsQuantiles(quant)
		out = append(out, op)
	}
	return out, rows.Err()
}

// nsQuantiles converts a quantiles() result (nanoseconds as float) into
// durations; missing entries stay zero.
func nsQuantiles(quant []float64) (p50, p95, p99 time.Duration) {
	get := func(i int) time.Duration {
		if i >= len(quant) {
			return 0
		}
		return time.Duration(quant[i])
	}
	return get(0), get(1), get(2)
}
