package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// OBI TCP-stats metric names (StatsO11y feature), keyed by the same k8s owner
// endpoints as obi.network.flow.bytes.
const (
	networkRTTMetric         = "obi.stat.tcp.rtt"                // histogram, seconds
	networkFailedMetric      = "obi.stat.tcp.failed.connections" // monotonic counter
	networkRetransmitsMetric = "obi.stat.tcp.retransmits"        // monotonic counter (OBI >= v0.12)
)

// NetworkEdgeHealth returns per-edge RTT p95 (ms), failed-connection counts and
// retransmits from OBI's TCP-stats metrics. RTT p95 is estimated from the merged OTLP
// explicit-bucket histogram: sum the bucket counts element-wise across the
// window, then take the upper bound of the bucket where the cumulative count
// first reaches 95% (capped at the last finite bound). Failed connections and
// retransmits are sums over cumulative counters — the same edge-weighting
// approximation NetworkEdges uses for bytes. Reads the otel_metrics_* tables;
// gate on infra-metrics.
//
// Retransmits need OBI >= v0.12, where the metric first exists. On an older
// sensor the query simply finds no rows and every edge reports zero, which is
// also what a healthy link reports — so the UI treats a zero as "nothing to
// say" rather than as a claim about the link.
func (s *Store) NetworkEdgeHealth(ctx context.Context, q storage.ServiceQuery) ([]storage.NetworkEdgeHealth, error) {
	tenants := tenantsOrDefault(q.Tenants, q.Tenant)
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
    WHERE Tenant IN (?)
      AND MetricName = ?
      AND TimeUnix >= ? AND TimeUnix < ?
      AND Attributes['k8s.src.owner.name'] != ''
      AND Attributes['k8s.dst.owner.name'] != ''
      AND Attributes['k8s.src.owner.name'] != Attributes['k8s.dst.owner.name']
    GROUP BY src, dst
)
WHERE length(bounds) > 0 AND arraySum(buckets) > 0`

	rows, err := s.conn.Query(ctx, rttQuery, tenants, networkRTTMetric, q.Range.Start, q.Range.End)
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

	// The two per-edge counters. Same shape, same attributes, same cumulative
	// -sum caveat — one query text, read twice, so a third TCP counter is a
	// line rather than another copy of this block.
	if err := s.sumEdgeCounter(ctx, tenants, networkFailedMetric, q.Range, func(src, dst string, v uint64) {
		get(src, dst).FailedConnections = v
	}); err != nil {
		return nil, fmt.Errorf("network edge failed conns: %w", err)
	}
	if err := s.sumEdgeCounter(ctx, tenants, networkRetransmitsMetric, q.Range, func(src, dst string, v uint64) {
		get(src, dst).Retransmits = v
	}); err != nil {
		return nil, fmt.Errorf("network edge retransmits: %w", err)
	}

	out := make([]storage.NetworkEdgeHealth, 0, len(byEdge))
	for _, e := range byEdge {
		out = append(out, *e)
	}
	return out, nil
}

// sumEdgeCounter reads one OBI TCP-stats counter per (src, dst) k8s owner pair
// and hands each row to `set`. Self-edges and unlabeled series are dropped, the
// same rule the flow edges apply.
func (s *Store) sumEdgeCounter(
	ctx context.Context, tenants []string, metric string, tr storage.TimeRange,
	set func(src, dst string, v uint64),
) error {
	const query = `
SELECT
    Attributes['k8s.src.owner.name'] AS src,
    Attributes['k8s.dst.owner.name'] AS dst,
    toUInt64(sum(Value))             AS total
FROM otel_metrics_sum
WHERE Tenant IN (?)
  AND MetricName = ?
  AND TimeUnix >= ? AND TimeUnix < ?
  AND Attributes['k8s.src.owner.name'] != ''
  AND Attributes['k8s.dst.owner.name'] != ''
  AND Attributes['k8s.src.owner.name'] != Attributes['k8s.dst.owner.name']
GROUP BY src, dst`

	rows, err := s.conn.Query(ctx, query, tenants, metric, tr.Start, tr.End)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var src, dst string
		var total uint64
		if err := rows.Scan(&src, &dst, &total); err != nil {
			return fmt.Errorf("scanning %s row: %w", metric, err)
		}
		set(src, dst, total)
	}
	return rows.Err()
}
