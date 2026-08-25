package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Istio control-plane metric names, verified against the istio source rather
// than a dashboard: pilot_xds and pilot_proxy_convergence_time in
// pilot/pkg/xds/monitoring.go, pilot_total_xds_rejects in
// pkg/xds/monitoring.go.
//
// Names arrive verbatim: the collector's prometheus receiver does not rewrite
// them, which the green module's Kepler series (kepler_node_cpu_joules_total,
// queried under exactly that name) already demonstrates on a real cluster.
const (
	meshConnectedProxiesMetric = "pilot_xds"                    // gauge: proxies connected to this istiod
	meshConvergenceMetric      = "pilot_proxy_convergence_time" // histogram, seconds
	meshRejectsMetric          = "pilot_total_xds_rejects"      // counter: config the proxies REFUSED
	meshPushesMetric           = "pilot_xds_pushes"             // counter: pushes attempted
)

// MeshControlPlane summarises whether the mesh's control plane is still
// programming the data plane over the window.
//
// The honest-absence rule lives here: with no series in the window, Available
// is false and every number stays zero — because "0 rejected configs" from a
// control plane nobody is scraping is precisely the reassuring lie this surface
// exists to prevent. Same discipline as the green module reporting "no RAPL"
// rather than 0 W.
//
// Reads the otel_metrics_* tables; callers must gate on infra-metrics.
func (s *Store) MeshControlPlane(ctx context.Context, q storage.ServiceQuery) (storage.MeshControlPlane, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
	var out storage.MeshControlPlane

	// Connected proxies is a gauge: the last value in the window is the answer,
	// not a sum over scrapes (which would multiply by however often we scraped).
	// count() decides availability, NOT the timestamp: an aggregate over no rows
	// returns max(TimeUnix) as the Unix epoch, which is a perfectly non-zero
	// time.Time in Go — so testing the timestamp would report every unscraped
	// control plane as available and healthy. The absence test exists because
	// that is exactly what this code did first.
	const gaugeQuery = `
SELECT argMax(Value, TimeUnix) AS latest, max(TimeUnix) AS newest, count() AS rows
FROM otel_metrics_gauge
WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?`
	row := s.conn.QueryRow(ctx, gaugeQuery, tenants, meshConnectedProxiesMetric, q.Range.Start, q.Range.End)
	var (
		proxies     float64
		newest      time.Time
		gaugePoints uint64
	)
	if err := row.Scan(&proxies, &newest, &gaugePoints); err != nil {
		return out, fmt.Errorf("mesh connected proxies: %w", err)
	}
	if gaugePoints > 0 {
		out.Available = true
		out.LastSeen = newest
		out.ConnectedProxies = uint64(proxies)
	}

	// Counters: sum over the window, the same cumulative-counter approximation
	// the flow and TCP-stats reads document.
	for _, c := range []struct {
		metric string
		set    func(uint64)
	}{
		{meshRejectsMetric, func(v uint64) { out.RejectedConfigs = v }},
		{meshPushesMetric, func(v uint64) { out.Pushes = v }},
	} {
		const sumQuery = `
SELECT toUInt64(sum(Value)) AS total, max(TimeUnix) AS newest, count() AS rows
FROM otel_metrics_sum
WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?`
		var (
			total    uint64
			latest   time.Time
			seenRows uint64
		)
		if err := s.conn.QueryRow(ctx, sumQuery, tenants, c.metric, q.Range.Start, q.Range.End).
			Scan(&total, &latest, &seenRows); err != nil {
			return out, fmt.Errorf("mesh %s: %w", c.metric, err)
		}
		if seenRows == 0 {
			continue
		}
		c.set(total)
		out.Available = true
		if latest.After(out.LastSeen) {
			out.LastSeen = latest
		}
	}

	// Convergence p95 from the merged histogram, the same bucket walk
	// NetworkEdgeHealth uses for RTT: sum bucket counts element-wise, then take
	// the upper bound where the cumulative count first reaches 95%.
	const histQuery = `
SELECT
    arrayElement(bounds, least(greatest(
        arrayFirstIndex(c -> c >= 0.95 * arraySum(buckets), arrayCumSum(buckets)), 1),
        length(bounds))) * 1000 AS p95_ms
FROM (
    SELECT sumForEach(BucketCounts) AS buckets, any(ExplicitBounds) AS bounds
    FROM otel_metrics_histogram
    WHERE Tenant IN (?) AND MetricName = ? AND TimeUnix >= ? AND TimeUnix < ?
)
WHERE length(bounds) > 0 AND arraySum(buckets) > 0`
	rows, err := s.conn.Query(ctx, histQuery, tenants, meshConvergenceMetric, q.Range.Start, q.Range.End)
	if err != nil {
		return out, fmt.Errorf("mesh convergence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p95 float64
		if err := rows.Scan(&p95); err != nil {
			return out, fmt.Errorf("scanning convergence row: %w", err)
		}
		out.ConvergenceP95Ms = p95
		out.Available = true
	}
	return out, rows.Err()
}
