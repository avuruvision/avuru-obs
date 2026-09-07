package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// defaultMeshDataplaneJob mirrors the chart's mesh.dataPlane.jobName default,
// for the same reason defaultMeshScrapeJob exists: a hub that was never told
// would otherwise look up the empty job and call a scraped data plane
// unconfigured.
const defaultMeshDataplaneJob = "mesh-dataplane"

// meshTargetsDownLimit bounds the named unreachable targets. Enough to act on;
// small enough that a dead fleet does not return the whole cluster.
const meshTargetsDownLimit = 10

// MeshSecurity reads what the proxies said about the traffic they carried:
// per destination workload and per edge, how much arrived over mTLS, how much
// in the clear, and who sent the clear part.
//
// One query does the counting. Its inner half computes each series' rise over
// the window (seriesDeltaExpr) — the counters are cumulative, so sum(Value)
// would report a proxy scraped ten times as ten times its traffic — and the
// outer half adds the series up per (metric, reporter, caller, destination,
// policy). The reporter preference is NOT in SQL: it is a rule about which
// side of an edge to believe, and it lives in Go (preferReporter) where it can
// be tested without a database.
//
// Reads the otel_metrics_* tables; callers gate on infra-metrics as for
// MeshControlPlane.
func (s *Store) MeshSecurity(ctx context.Context, q storage.ServiceQuery) (storage.MeshSecurity, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	job := q.MeshDataplaneJob
	if job == "" {
		job = defaultMeshDataplaneJob
	}
	out := storage.MeshSecurity{State: storage.MeshControlPlaneUnconfigured}

	rows, err := s.meshTrafficRows(ctx, tenants, q.Range)
	if err != nil {
		return out, err
	}
	if len(rows) > 0 {
		out.Available = true
		out.State = storage.MeshControlPlaneOK
		out.Workloads, out.Edges = preferReporter(rows)
		for _, r := range rows {
			if r.Newest.After(out.LastSeen) {
				out.LastSeen = r.Newest
			}
		}
	} else {
		// Nothing recognised: the scrape-report series says which of the three
		// silences this is, exactly as for the control plane.
		state, err := s.meshScrapeState(ctx, tenants, job, q.Range)
		if err != nil {
			return out, err
		}
		out.State = state
	}

	if err := s.meshTargets(ctx, tenants, job, q.Range, &out); err != nil {
		return out, err
	}
	return out, nil
}

// meshTrafficRows runs the delta query over both data-plane counters.
// Destinations the proxy could not name are dropped here rather than in Go:
// "unknown" is Istio's word for traffic leaving the mesh, and it is not a
// workload whose security anyone can fix.
func (s *Store) meshTrafficRows(ctx context.Context, tenants []string, tr storage.TimeRange) ([]meshTrafficRow, error) {
	query := fmt.Sprintf(`
WITH deltas AS (
    SELECT
        MetricName                                   AS metric,
        Attributes['reporter']                       AS reporter,
        Attributes['source_workload_namespace']      AS src_ns,
        Attributes['source_workload']                AS src,
        Attributes['destination_workload_namespace'] AS dst_ns,
        Attributes['destination_workload']           AS dst,
        Attributes['connection_security_policy']     AS policy,
        %s                                           AS sid,
        %s                                           AS delta,
        max(TimeUnix)                                AS newest
    FROM otel_metrics_sum
    WHERE Tenant IN (?) AND MetricName IN (?, ?)
      AND TimeUnix >= ? AND TimeUnix < ?
      AND Attributes['destination_workload'] != ''
      AND Attributes['destination_workload'] != 'unknown'
    GROUP BY metric, reporter, src_ns, src, dst_ns, dst, policy, sid
)
SELECT metric, reporter, src_ns, src, dst_ns, dst, policy,
       toUInt64(sum(delta)) AS n, count() AS series, max(newest) AS newest
FROM deltas
GROUP BY metric, reporter, src_ns, src, dst_ns, dst, policy
ORDER BY metric, reporter, src_ns, src, dst_ns, dst, policy`, seriesIDExpr, seriesDeltaExpr)

	rows, err := s.conn.Query(ctx, query, tenants, meshRequestsMetric, meshTCPOpenedMetric, tr.Start, tr.End)
	if err != nil {
		return nil, fmt.Errorf("mesh traffic: %w", err)
	}
	defer rows.Close()
	var out []meshTrafficRow
	for rows.Next() {
		var r meshTrafficRow
		if err := rows.Scan(&r.Metric, &r.Reporter, &r.SrcNS, &r.Src, &r.DstNS, &r.Dst, &r.Policy,
			&r.N, &r.Series, &r.Newest); err != nil {
			return nil, fmt.Errorf("scanning mesh traffic row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// meshTargets fills the scrape's own report of which proxies it reached: one
// `up` series per target, the latest sample deciding. Targets are keyed by
// service.instance.id (the receiver's ip:port per scraped pod), and named by
// k8s.namespace.name / k8s.pod.name — RESOURCE attributes here, not point
// attributes: the sensor's groupbyattrs processor lifts them off the points,
// which is why they are read from ResourceAttributes alongside service.name.
func (s *Store) meshTargets(
	ctx context.Context, tenants []string, job string, tr storage.TimeRange, out *storage.MeshSecurity,
) error {
	const query = `
SELECT
    ResourceAttributes['service.instance.id']     AS instance,
    any(ResourceAttributes['k8s.namespace.name']) AS ns,
    any(ResourceAttributes['k8s.pod.name'])       AS pod,
    argMax(Value, TimeUnix)                       AS latest
FROM otel_metrics_gauge
WHERE Tenant IN (?) AND MetricName = 'up'
  AND ResourceAttributes['service.name'] = ?
  AND TimeUnix >= ? AND TimeUnix < ?
GROUP BY instance
ORDER BY instance`
	rows, err := s.conn.Query(ctx, query, tenants, job, tr.Start, tr.End)
	if err != nil {
		return fmt.Errorf("mesh targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var instance, ns, pod string
		var latest float64
		if err := rows.Scan(&instance, &ns, &pod, &latest); err != nil {
			return fmt.Errorf("scanning mesh target row: %w", err)
		}
		out.TargetsTotal++
		if latest > 0 {
			out.TargetsUp++
			continue
		}
		if len(out.TargetsDown) < meshTargetsDownLimit {
			out.TargetsDown = append(out.TargetsDown, meshTargetName(instance, ns, pod))
		}
	}
	return rows.Err()
}

// meshTargetName is "namespace/pod" when the resource carried both, and the
// scrape instance otherwise — an unnamed target is still a target to look at.
func meshTargetName(instance, ns, pod string) string {
	if pod == "" {
		return instance
	}
	return ns + "/" + pod
}

// meshLatestGauges reads one or more gauges at their latest value per scraped
// instance. The caller adds the instances up: a gauge is a fact about one pod,
// and the fleet's figure is the sum of the latest facts, never the sum of the
// samples.
func (s *Store) meshLatestGauges(
	ctx context.Context, tenants []string, metrics []string, tr storage.TimeRange,
	each func(metric, instance string, latest float64),
) error {
	query := fmt.Sprintf(`
SELECT MetricName, ResourceAttributes['service.instance.id'] AS instance,
       argMax(Value, TimeUnix) AS latest
FROM otel_metrics_gauge
WHERE Tenant IN (?) AND MetricName IN (%s) AND TimeUnix >= ? AND TimeUnix < ?
GROUP BY MetricName, instance`, inList(len(metrics)))
	args := []any{tenants}
	for _, m := range metrics {
		args = append(args, m)
	}
	args = append(args, tr.Start, tr.End)
	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mesh gauges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var metric, instance string
		var latest float64
		if err := rows.Scan(&metric, &instance, &latest); err != nil {
			return fmt.Errorf("scanning mesh gauge row: %w", err)
		}
		each(metric, instance, latest)
	}
	return rows.Err()
}
