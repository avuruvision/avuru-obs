package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Green energy queries (module green): read-time counter-delta aggregation of
// the Kepler cumulative-joule series over otel_metrics_sum, the service-health
// / network-health precedent — no owned tables, no rollup jobs. Storage
// returns Wh only; carbon factors never enter SQL (AEP: gCO2e is query-time
// math in the green module).

// greenSeriesID isolates one exported counter series: metric name plus the
// full attribute AND resource-attribute identity. Deltas are computed per
// series FIRST and summed after, so multi-zone / multi-container / multi-
// metric series can never cross-cancel each other's max−min. mapSort: a Map
// column preserves insertion order and writers don't guarantee a stable pair
// order, so hashing must be order-insensitive or one series splits into many
// (every delta collapsing to 0).
const greenSeriesID = "cityHash64(MetricName, toString(mapSort(Attributes)), toString(mapSort(ResourceAttributes)))"

// greenBucket resolves the series bucket width: the caller's interval, or the
// infra default (Range/defaultSeriesPoints), floored at one second.
func greenBucket(q storage.GreenQuery) time.Duration {
	b := q.Interval
	if b <= 0 {
		b = q.Range.End.Sub(q.Range.Start) / defaultSeriesPoints
	}
	if b < time.Second {
		b = time.Second
	}
	return b
}

// inList renders n bound placeholders for a SQL IN clause.
func inList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// ServiceEnergy aggregates the per-pod cumulative energy counters to
// per-service Wh totals + bucketed series, heaviest first.
//
// series_deltas: max(Value)−min(Value) joules per unique series
// (greenSeriesID) per bucket. Counter-reset containment comes from the
// PER-BUCKET grouping, not the clamp: each bucket's delta is computed
// independently, so a reset corrupts at most its own bucket — and a reset
// WITHIN a bucket OVERCOUNTS by up to the pre-reset value (max stays the
// pre-reset high-water mark while min is the post-reset low). greatest(…, 0)
// is purely defensive — max ≥ min always holds inside a group, so it can
// never fire; kept as a guard against future reshapes of the expression.
// This, plus delta lost across bucket boundaries, is the documented
// cumulative-counter approximation (AEP). pod_workloads: the kubeletstats
// pod→workload map from k8s.pod.cpu.usage resource attributes (owner
// precedence deployment > statefulset > daemonset — workloadExpr, shared
// with ListPodStats; the workload name IS the service name). The LEFT JOIN
// sends energy whose pod has no workload match to the empty service name —
// the unattributed bucket (unmatched rows read as the empty string under
// ClickHouse's default join_use_nulls = 0; the hub owns its connection, so
// the default holds). Requires the infra-metrics module (the join's source).
func (s *Store) ServiceEnergy(ctx context.Context, q storage.GreenQuery) ([]storage.ServiceEnergy, error) {
	if len(q.PodEnergyMetrics) == 0 {
		return nil, nil
	}
	bucket := greenBucket(q)

	query := fmt.Sprintf(`
WITH series_deltas AS (
    SELECT
        Attributes[?]                                    AS pod,
        Attributes[?]                                    AS ns,
        Attributes['avuruops_quality']                   AS quality,
        toStartOfInterval(TimeUnix, INTERVAL %d SECOND)  AS t,
        %s                                               AS sid,
        greatest(max(Value) - min(Value), 0)             AS joules
    FROM otel_metrics_sum
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (%s)
    GROUP BY pod, ns, quality, t, sid
),
pod_workloads AS (
    SELECT
        ResourceAttributes['k8s.pod.name']       AS pod,
        ResourceAttributes['k8s.namespace.name'] AS ns,
        anyLast(%s) AS workload
    FROM otel_metrics_gauge
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName = ?
      AND ResourceAttributes['k8s.pod.name'] != ''
    GROUP BY pod, ns
)
SELECT w.workload AS service, d.quality AS quality, d.t AS t, sum(d.joules) / 3600 AS wh
FROM series_deltas AS d
LEFT JOIN pod_workloads AS w ON d.pod = w.pod AND d.ns = w.ns
GROUP BY service, quality, t
ORDER BY service, quality, t`,
		int(bucket.Seconds()), greenSeriesID, inList(len(q.PodEnergyMetrics)), workloadExpr)

	args := []any{q.PodNameAttr, q.PodNamespaceAttr, q.Tenant, q.Range.Start, q.Range.End}
	for _, m := range q.PodEnergyMetrics {
		args = append(args, m)
	}
	args = append(args, q.Tenant, q.Range.Start, q.Range.End, metricPodCPU)

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("service energy: %w", err)
	}
	defer rows.Close()

	var (
		out []storage.ServiceEnergy
		cur *storage.ServiceEnergy
	)
	for rows.Next() {
		var (
			service string
			quality string
			t       time.Time
			wh      float64
		)
		if err := rows.Scan(&service, &quality, &t, &wh); err != nil {
			return nil, fmt.Errorf("scanning service energy row: %w", err)
		}
		if cur == nil || cur.Service != service || cur.Quality != quality {
			out = append(out, storage.ServiceEnergy{Service: service, Quality: quality})
			cur = &out[len(out)-1]
		}
		cur.WattHours += wh
		cur.Points = append(cur.Points, storage.EnergyPoint{Time: t, WattHours: wh})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WattHours != out[j].WattHours {
			return out[i].WattHours > out[j].WattHours
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Quality < out[j].Quality
	})
	return out, nil
}

// NodeEnergy aggregates the per-node cumulative energy counters to per-node
// Wh totals + bucketed series, heaviest first — the same per-series delta
// math and reset semantics as ServiceEnergy (per-bucket grouping contains
// resets; a within-bucket reset overcounts; the clamp is defensive-only),
// keyed by the standard k8s.node.name resource
// attribute (nodeAttr: the sensor pipeline upserts it on every metric, the
// same key the whole infra view relies on — deliberately not config-driven,
// matching ListNodeStats/ListAgentNodes). Rows without it are excluded, the
// sibling convention.
func (s *Store) NodeEnergy(ctx context.Context, q storage.GreenQuery) ([]storage.NodeEnergy, error) {
	if len(q.NodeEnergyMetrics) == 0 {
		return nil, nil
	}
	bucket := greenBucket(q)

	query := fmt.Sprintf(`
SELECT node, quality, t, sum(joules) / 3600 AS wh
FROM (
    SELECT
        `+nodeAttr+`                                     AS node,
        Attributes['avuruops_quality']                   AS quality,
        toStartOfInterval(TimeUnix, INTERVAL %d SECOND)  AS t,
        %s                                               AS sid,
        greatest(max(Value) - min(Value), 0)             AS joules
    FROM otel_metrics_sum
    WHERE Tenant = ? AND TimeUnix >= ? AND TimeUnix < ?
      AND MetricName IN (%s)
      AND `+nodeAttr+` != ''
    GROUP BY node, quality, t, sid
)
GROUP BY node, quality, t
ORDER BY node, quality, t`,
		int(bucket.Seconds()), greenSeriesID, inList(len(q.NodeEnergyMetrics)))

	args := []any{q.Tenant, q.Range.Start, q.Range.End}
	for _, m := range q.NodeEnergyMetrics {
		args = append(args, m)
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("node energy: %w", err)
	}
	defer rows.Close()

	var (
		out []storage.NodeEnergy
		cur *storage.NodeEnergy
	)
	for rows.Next() {
		var (
			node    string
			quality string
			t       time.Time
			wh      float64
		)
		if err := rows.Scan(&node, &quality, &t, &wh); err != nil {
			return nil, fmt.Errorf("scanning node energy row: %w", err)
		}
		if cur == nil || cur.Node != node || cur.Quality != quality {
			out = append(out, storage.NodeEnergy{Node: node, Quality: quality})
			cur = &out[len(out)-1]
		}
		cur.WattHours += wh
		cur.Points = append(cur.Points, storage.EnergyPoint{Time: t, WattHours: wh})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WattHours != out[j].WattHours {
			return out[i].WattHours > out[j].WattHours
		}
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Quality < out[j].Quality
	})
	return out, nil
}
