package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// OBI TCP-stats metric names (StatsO11y feature), keyed by the same k8s owner
// endpoints as obi.network.flow.bytes.
const (
	networkRTTMetric    = "obi.stat.tcp.rtt"                // histogram, seconds
	networkFailedMetric = "obi.stat.tcp.failed.connections" // monotonic counter
)

// NetworkEdgeHealth returns per-edge RTT p95 (ms) and failed-connection counts
// from OBI's TCP-stats metrics. RTT p95 is estimated from the merged OTLP
// explicit-bucket histogram: sum the bucket counts element-wise across the
// window, then take the upper bound of the bucket where the cumulative count
// first reaches 95% (capped at the last finite bound). Failed connections are a
// sum over the cumulative counter — the same edge-weighting approximation
// NetworkEdges uses for bytes. Reads the otel_metrics_* tables; gate on
// infra-metrics.
func (s *Store) NetworkEdgeHealth(ctx context.Context, q storage.ServiceQuery) ([]storage.NetworkEdgeHealth, error) {
	byEdge := map[[2]string]*storage.NetworkEdgeHealth{}
	get := func(src, dst string) *storage.NetworkEdgeHealth {
		k := [2]string{src, dst}
		if e, ok := byEdge[k]; ok {
			return e
		}
		e := &storage.NetworkEdgeHealth{Source: src, Target: dst}
		byEdge[k] = e
		return e
	}

	// RTT p95 (ms) per edge from the histogram.
	const rttQuery = `
SELECT src, dst,
    arrayElement(bounds, least(greatest(
        arrayFirstIndex(c -> c >= 0.95 * arraySum(buckets), arrayCumSum(buckets)), 1),
        length(bounds))) * 1000 AS p95_ms
FROM (
    SELECT
        Attributes['k8s.src.owner.name'] AS src,
        Attributes['k8s.dst.owner.name'] AS dst,
        sumForEach(BucketCounts)         AS buckets,
        any(ExplicitBounds)              AS bounds
    FROM otel_metrics_histogram
    WHERE Tenant = ?
      AND MetricName = ?
      AND TimeUnix >= ? AND TimeUnix < ?
      AND Attributes['k8s.src.owner.name'] != ''
      AND Attributes['k8s.dst.owner.name'] != ''
      AND Attributes['k8s.src.owner.name'] != Attributes['k8s.dst.owner.name']
    GROUP BY src, dst
)
WHERE length(bounds) > 0 AND arraySum(buckets) > 0`

	rows, err := s.conn.Query(ctx, rttQuery, q.Tenant, networkRTTMetric, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("network edge rtt: %w", err)
	}
	for rows.Next() {
		var src, dst string
		var p95 float64
		if err := rows.Scan(&src, &dst, &p95); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning rtt row: %w", err)
		}
		get(src, dst).RTTMs = p95
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Failed connections per edge from the counter.
	const failedQuery = `
SELECT
    Attributes['k8s.src.owner.name'] AS src,
    Attributes['k8s.dst.owner.name'] AS dst,
    toUInt64(sum(Value))             AS failed
FROM otel_metrics_sum
WHERE Tenant = ?
  AND MetricName = ?
  AND TimeUnix >= ? AND TimeUnix < ?
  AND Attributes['k8s.src.owner.name'] != ''
  AND Attributes['k8s.dst.owner.name'] != ''
  AND Attributes['k8s.src.owner.name'] != Attributes['k8s.dst.owner.name']
GROUP BY src, dst`

	frows, err := s.conn.Query(ctx, failedQuery, q.Tenant, networkFailedMetric, q.Range.Start, q.Range.End)
	if err != nil {
		return nil, fmt.Errorf("network edge failed conns: %w", err)
	}
	defer frows.Close()
	for frows.Next() {
		var src, dst string
		var failed uint64
		if err := frows.Scan(&src, &dst, &failed); err != nil {
			return nil, fmt.Errorf("scanning failed-conn row: %w", err)
		}
		get(src, dst).FailedConnections = failed
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}

	out := make([]storage.NetworkEdgeHealth, 0, len(byEdge))
	for _, e := range byEdge {
		out = append(out, *e)
	}
	return out, nil
}
