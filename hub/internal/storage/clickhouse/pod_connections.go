package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// PodConnections attributes requests only to identities present on spans.
// A child without a Pod identity leaves the destination unresolved. Ambiguous
// children (e.g. a reused parent span) are not assigned to an arbitrary replica.
// Joining on Tenant as well as trace/span IDs prevents colliding trace IDs in
// two member tenants from fabricating a Pod connection.
func (s *Store) PodConnections(ctx context.Context, q storage.InfraQuery) ([]storage.PodConnection, error) {
	limit := q.Limit
	if limit <= 0 || limit > 501 {
		limit = 201
	}
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	query := `
WITH children AS (
    SELECT Tenant, TraceId, ParentSpanId,
        any(ResourceAttributes['k8s.pod.name']) AS pod,
        any(ResourceAttributes['k8s.namespace.name']) AS ns,
        any(ResourceAttributes['k8s.node.name']) AS node,
        any(ServiceName) AS service
    FROM otel_traces
    WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind = 'Server' AND ParentSpanId != ''
      AND ResourceAttributes['k8s.pod.name'] != ''
    GROUP BY Tenant, TraceId, ParentSpanId
    HAVING uniqExact(tuple(ResourceAttributes['k8s.pod.name'],
        ResourceAttributes['k8s.namespace.name'], ResourceAttributes['k8s.node.name'])) = 1
), callers AS (
    SELECT Tenant, TraceId, SpanId,
        argMax(tuple(ResourceAttributes, ServiceName, SpanAttributes, Duration,
            ` + errorSpanExpr("") + `), Timestamp) AS data
    FROM otel_traces
    WHERE Tenant IN (?) AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind = 'Client'
      AND ResourceAttributes['k8s.pod.name'] != ''` + auxExclusion("") + `
    GROUP BY Tenant, TraceId, SpanId
)
SELECT
    caller.data.1['k8s.pod.name'] AS src,
    caller.data.1['k8s.namespace.name'] AS src_ns,
    caller.data.1['k8s.node.name'] AS src_node,
    caller.data.2 AS src_service,
    child.pod AS dst, child.ns AS dst_ns, child.node AS dst_node,
    child.service AS dst_service,
    if(child.pod != '', '', multiIf(
        caller.data.3['server.address'] != '', caller.data.3['server.address'],
        caller.data.3['net.peer.name'] != '', caller.data.3['net.peer.name'],
        caller.data.3['net.peer.ip'] != '', caller.data.3['net.peer.ip'], '')) AS peer,
    count() AS calls, countIf(caller.data.5) AS errors,
    quantile(0.95)(toFloat64(caller.data.4)) AS p95
FROM callers AS caller
LEFT JOIN children AS child ON caller.Tenant = child.Tenant
    AND caller.TraceId = child.TraceId AND caller.SpanId = child.ParentSpanId
WHERE (dst != '' OR peer != '')`
	args := []any{tenants, q.Range.Start, q.Range.End, tenants, q.Range.Start, q.Range.End}
	if q.Node != "" {
		query += ` AND (src_node = ? OR dst_node = ?)`
		args = append(args, q.Node, q.Node)
	}
	query += `
GROUP BY src, src_ns, src_node, src_service, dst, dst_ns, dst_node, dst_service, peer
ORDER BY calls DESC, src, src_ns, dst, dst_ns, peer
LIMIT ? SETTINGS join_use_nulls = 0, max_execution_time = 15`
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pod connections: %w", err)
	}
	defer rows.Close()
	var out []storage.PodConnection
	for rows.Next() {
		var edge storage.PodConnection
		var p95 float64
		if err := rows.Scan(&edge.Source.Name, &edge.Source.Namespace, &edge.Source.Node, &edge.Source.Service,
			&edge.Target.Name, &edge.Target.Namespace, &edge.Target.Node, &edge.Target.Service,
			&edge.Peer, &edge.Calls, &edge.Errors, &p95); err != nil {
			return nil, fmt.Errorf("scanning pod connection: %w", err)
		}
		edge.P95 = time.Duration(p95)
		out = append(out, edge)
	}
	return out, rows.Err()
}
