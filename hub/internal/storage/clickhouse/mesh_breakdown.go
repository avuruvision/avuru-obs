package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// ztunnel's own series, verified against the ztunnel source (src/metrics and
// src/xds). The two gauges are what the node proxy is carrying; the counter is
// how often its control-plane stream was cut.
const (
	meshZtunnelActiveMetric       = "workload_manager_active_proxy_count"     // gauge
	meshZtunnelPendingMetric      = "workload_manager_pending_proxy_count"    // gauge
	meshZtunnelTerminationsMetric = "istio_xds_connection_terminations_total" // counter
)

// MeshRequestBreakdown reads one destination workload's requests over the
// window by the dimensions the proxy attaches to each: response flag,
// destination version, caller and response code. Same delta discipline as
// MeshSecurity, the same reporter preference applied after (foldRequests),
// and restricted to the one destination in SQL so a busy mesh does not pay
// for the whole fleet to answer for one workload.
func (s *Store) MeshRequestBreakdown(
	ctx context.Context, q storage.ServiceQuery, namespace, workload string,
) (storage.MeshRequestBreakdown, error) {
	query := fmt.Sprintf(`
WITH deltas AS (
    SELECT
        Attributes['reporter']                  AS reporter,
        Attributes['source_workload_namespace'] AS src_ns,
        Attributes['source_workload']           AS src,
        Attributes['response_flags']            AS flags,
        Attributes['response_code']             AS code,
        Attributes['destination_version']       AS version,
        %s                                      AS sid,
        %s                                      AS delta
    FROM otel_metrics_sum
    WHERE Tenant IN (?) AND MetricName = ?
      AND TimeUnix >= ? AND TimeUnix < ?
      AND Attributes['destination_workload_namespace'] = ?
      AND Attributes['destination_workload'] = ?
    GROUP BY reporter, src_ns, src, flags, code, version, sid
)
SELECT reporter, src_ns, src, flags, code, version, toUInt64(sum(delta)) AS n
FROM deltas
GROUP BY reporter, src_ns, src, flags, code, version
ORDER BY reporter, src_ns, src, flags, code, version`, seriesIDExpr, seriesDeltaExpr)

	rows, err := s.conn.Query(ctx, query, tenantsOrDefault(q.Tenants, q.Tenant), meshRequestsMetric,
		q.Range.Start, q.Range.End, namespace, workload)
	if err != nil {
		return storage.MeshRequestBreakdown{}, fmt.Errorf("mesh request breakdown: %w", err)
	}
	defer rows.Close()
	var cells []meshRequestRow
	for rows.Next() {
		var r meshRequestRow
		if err := rows.Scan(&r.Reporter, &r.SrcNS, &r.Src, &r.Flags, &r.Code, &r.Version, &r.N); err != nil {
			return storage.MeshRequestBreakdown{}, fmt.Errorf("scanning mesh request row: %w", err)
		}
		cells = append(cells, r)
	}
	if err := rows.Err(); err != nil {
		return storage.MeshRequestBreakdown{}, err
	}
	return foldRequests(cells), nil
}

// MeshZtunnelHealth sums the node proxies' own gauges at their latest value
// per pod, and the terminations counter's rise over the window. Pods is how
// many ztunnel instances reported at all — the number a five-node cluster
// checks against.
//
// The gauges are not filtered by scrape job: their names belong to ztunnel
// alone, and an install that scrapes ztunnel under a job of its own should
// still be read.
func (s *Store) MeshZtunnelHealth(ctx context.Context, q storage.ServiceQuery) (storage.MeshZtunnelHealth, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	var out storage.MeshZtunnelHealth

	pods := map[string]struct{}{}
	err := s.meshLatestGauges(ctx, tenants, []string{meshZtunnelActiveMetric, meshZtunnelPendingMetric}, q.Range,
		func(metric, instance string, latest float64) {
			out.Measured = true
			pods[instance] = struct{}{}
			switch metric {
			case meshZtunnelActiveMetric:
				out.ActiveWorkloads += uint64(latest)
			case meshZtunnelPendingMetric:
				out.PendingWorkloads += uint64(latest)
			}
		})
	if err != nil {
		return out, fmt.Errorf("ztunnel gauges: %w", err)
	}
	out.Pods = uint64(len(pods))

	query := fmt.Sprintf(`
SELECT toUInt64(sum(delta)) AS n, count() AS series
FROM (
    SELECT %s AS sid, %s AS delta
    FROM otel_metrics_sum
    WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?
    GROUP BY sid
)`, seriesIDExpr, seriesDeltaExpr)
	var (
		terminations uint64
		series       uint64
	)
	if err := s.conn.QueryRow(ctx, query, tenants, meshZtunnelTerminationsMetric, q.Range.Start, q.Range.End).
		Scan(&terminations, &series); err != nil {
		return out, fmt.Errorf("ztunnel terminations: %w", err)
	}
	if series > 0 {
		out.Measured = true
		out.XDSConnectionTerminations = terminations
	}
	return out, nil
}
